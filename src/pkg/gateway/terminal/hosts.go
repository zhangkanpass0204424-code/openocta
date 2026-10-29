package terminal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

var ErrHostNotFound = errors.New("terminal host not found")

type SavedHost struct {
	ID          string `json:"id"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	Fingerprint string `json:"fingerprint"`
}

type HostStore struct {
	mu    sync.Mutex
	path  string
	hosts []SavedHost
}

func NewHostStore(path string) (*HostStore, error) {
	store := &HostStore{path: path}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return store, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &store.hosts); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *HostStore) List() []SavedHost {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]SavedHost{}, s.hosts...)
}

func (s *HostStore) Upsert(host SavedHost) (SavedHost, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	previous := append([]SavedHost(nil), s.hosts...)
	for index, saved := range s.hosts {
		if saved.Host == host.Host && saved.Port == host.Port && saved.Username == host.Username {
			host.ID = saved.ID
			s.hosts[index] = host
			if err := s.saveLocked(); err != nil {
				s.hosts = previous
				return SavedHost{}, err
			}
			return host, nil
		}
	}

	host.ID = uuid.NewString()
	s.hosts = append(s.hosts, host)
	if err := s.saveLocked(); err != nil {
		s.hosts = previous
		return SavedHost{}, err
	}
	return host, nil
}

func (s *HostStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := append([]SavedHost(nil), s.hosts...)
	for index, host := range s.hosts {
		if host.ID != id {
			continue
		}
		s.hosts = append(s.hosts[:index], s.hosts[index+1:]...)
		if err := s.saveLocked(); err != nil {
			s.hosts = previous
			return err
		}
		return nil
	}
	return ErrHostNotFound
}

func (s *HostStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.hosts, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0600)
}
