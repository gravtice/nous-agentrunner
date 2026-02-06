package runnerd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Server struct {
	ctx context.Context
	cfg Config

	mu                    sync.Mutex
	guestRunnerSyncMu     sync.Mutex
	guestRunnerSyncSum    string
	guestRunnerStageSize  int64
	guestRunnerStageMTime int64
	guestRunnerStageSum   string
	skillsMu              sync.Mutex
	shares                []shareEntry
	shareExcludes         []excludeEntry // effective (user + builtin)
	shareUserExcludes     []excludeEntry // persisted in shares.json
	services              map[string]Service
	tunnels               map[string]*tunnelEntry
	tunnelByHostPort      map[int]string
	forwards              map[string]*forwardEntry
	forwardByGuestPort    map[int]string
	vmRestartRequired     bool
	activeServiceChats    map[string]bool // service_id -> connected
}

func NewServer(ctx context.Context, cfg Config) (*Server, error) {
	s := &Server{
		ctx:      ctx,
		cfg:      cfg,
		services: make(map[string]Service),
		tunnels:  make(map[string]*tunnelEntry),
		// host_port -> tunnel_id (for idempotent creation)
		tunnelByHostPort: make(map[int]string),
		forwards:         make(map[string]*forwardEntry),
		// guest_port -> forward_id (for idempotent creation)
		forwardByGuestPort: make(map[int]string),
	}
	if err := s.loadState(); err != nil {
		return nil, err
	}
	return s, nil
}

type shareEntry struct {
	Share
	CanonicalHostPath string
}

type excludeEntry struct {
	HostPath          string
	CanonicalHostPath string
}

type Share struct {
	ShareID  string `json:"share_id"`
	HostPath string `json:"host_path"`
}

type Service struct {
	ServiceID          string `json:"service_id"`
	SessionID          string `json:"session_id,omitempty"`
	Type               string `json:"type"`
	ImageRef           string `json:"image_ref"`
	State              string `json:"state"`
	CreatedAt          string `json:"created_at"`
	IdleTimeoutSeconds int    `json:"idle_timeout_seconds,omitempty"`
	LastActivityAt     string `json:"last_activity_at,omitempty"`
	StopReason         string `json:"stop_reason,omitempty"`
}

type sharesFile struct {
	Shares   []Share  `json:"shares"`
	Excludes []string `json:"excludes,omitempty"`
}

type servicesFile struct {
	Services []Service `json:"services"`
}

func (s *Server) sharesPath() string { return filepath.Join(s.cfg.Paths.AppSupportDir, "shares.json") }
func (s *Server) servicesPath() string {
	return filepath.Join(s.cfg.Paths.AppSupportDir, "services.json")
}

func (s *Server) loadState() error {
	if err := s.loadShares(); err != nil {
		return err
	}
	if err := s.loadServices(); err != nil {
		return err
	}
	return nil
}

func (s *Server) loadShares() error {
	b, readErr := os.ReadFile(s.sharesPath())
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("read shares: %w", readErr)
	}

	var sf sharesFile
	if readErr == nil {
		if err := json.Unmarshal(b, &sf); err != nil {
			return fmt.Errorf("parse shares: %w", err)
		}
	}

	changedShares, entries, err := normalizeShares(sf.Shares, s.cfg.Paths.DefaultSharedTmpDir)
	if err != nil {
		return err
	}

	changedUserExcludes, userExcludes, err := normalizeShareExcludes(sf.Excludes, entries, s.cfg.Paths.DefaultSharedTmpDir)
	if err != nil {
		return err
	}

	builtinExcludes := builtinShareExcludes(entries, s.cfg.Paths.DefaultSharedTmpDir)
	stripChanged, userExcludes := stripUserExcludesUnderBuiltin(userExcludes, builtinExcludes)

	s.shares = entries
	s.shareUserExcludes = userExcludes
	s.shareExcludes = mergeExcludeEntries(userExcludes, builtinExcludes)

	if changedShares || changedUserExcludes || stripChanged {
		return s.saveSharesLocked()
	}
	return nil
}

func (s *Server) saveSharesLocked() error {
	sf := sharesFile{
		Shares:   make([]Share, 0, len(s.shares)),
		Excludes: make([]string, 0, len(s.shareUserExcludes)),
	}
	for _, e := range s.shares {
		sf.Shares = append(sf.Shares, e.Share)
	}
	for _, e := range s.shareUserExcludes {
		sf.Excludes = append(sf.Excludes, e.HostPath)
	}
	tmp := s.sharesPath() + ".tmp"
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.sharesPath())
}

func (s *Server) loadServices() error {
	b, err := os.ReadFile(s.servicesPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read services: %w", err)
	}
	var sf servicesFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return fmt.Errorf("parse services: %w", err)
	}
	for _, svc := range sf.Services {
		if svc.ServiceID == "" {
			continue
		}
		s.services[svc.ServiceID] = svc
	}
	return nil
}

func (s *Server) saveServicesLocked() error {
	sf := servicesFile{Services: make([]Service, 0, len(s.services))}
	for _, svc := range s.services {
		sf.Services = append(sf.Services, svc)
	}
	tmp := s.servicesPath() + ".tmp"
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.servicesPath())
}

func nowISO8601() string { return time.Now().UTC().Format(time.RFC3339Nano) }
