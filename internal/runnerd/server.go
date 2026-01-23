package runnerd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Server struct {
	cfg Config

	mu                sync.Mutex
	shares            []shareEntry
	services          map[string]Service
	vmRestartRequired bool
}

func NewServer(cfg Config) (*Server, error) {
	s := &Server{
		cfg:      cfg,
		services: make(map[string]Service),
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

type Share struct {
	ShareID  string `json:"share_id"`
	HostPath string `json:"host_path"`
}

type Service struct {
	ServiceID string `json:"service_id"`
	Type      string `json:"type"`
	ImageRef  string `json:"image_ref"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"`
}

type sharesFile struct {
	Shares []Share `json:"shares"`
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
	b, err := os.ReadFile(s.sharesPath())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read shares: %w", err)
	}

	var sf sharesFile
	if err == nil {
		if err := json.Unmarshal(b, &sf); err != nil {
			return fmt.Errorf("parse shares: %w", err)
		}
	}

	changed, entries, err := normalizeShares(sf.Shares, s.cfg.Paths.DefaultSharedTmpDir)
	if err != nil {
		return err
	}
	s.shares = entries
	if changed || errors.Is(err, os.ErrNotExist) {
		return s.saveSharesLocked()
	}
	return nil
}

func (s *Server) saveSharesLocked() error {
	sf := sharesFile{Shares: make([]Share, 0, len(s.shares))}
	for _, e := range s.shares {
		sf.Shares = append(sf.Shares, e.Share)
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
