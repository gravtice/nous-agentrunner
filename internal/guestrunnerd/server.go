package guestrunnerd

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
	mu     sync.Mutex
	state  State
	config Config

	tunnels          map[string]*tunnelEntry
	tunnelByHostPort map[int]string
}

type Config struct {
	ListenAddr string
	ListenPort int
	StateDir   string

	HostTunnelVsockPort int
}

type State struct {
	Services map[string]Service `json:"services"`
}

type Service struct {
	ServiceID     string `json:"service_id"`
	Type          string `json:"type"`
	ImageRef      string `json:"image_ref"`
	ContainerName string `json:"container_name"`
	Port          int    `json:"port"`
	State         string `json:"state"`
	CreatedAt     string `json:"created_at"`
}

func NewServer(cfg Config) (*Server, error) {
	if cfg.StateDir == "" {
		return nil, errors.New("state dir is required")
	}
	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return nil, fmt.Errorf("mkdir %q: %w", cfg.StateDir, err)
	}
	s := &Server{
		config: cfg,
		state: State{
			Services: make(map[string]Service),
		},
		tunnels:          make(map[string]*tunnelEntry),
		tunnelByHostPort: make(map[int]string),
	}
	if err := s.loadState(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) statePath() string { return filepath.Join(s.config.StateDir, "state.json") }

func (s *Server) loadState() error {
	b, err := os.ReadFile(s.statePath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	if st.Services == nil {
		st.Services = make(map[string]Service)
	}
	s.state = st
	return nil
}

func (s *Server) saveStateLocked() error {
	tmp := s.statePath() + ".tmp"
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.statePath())
}

func nowISO8601() string { return time.Now().UTC().Format(time.RFC3339Nano) }
