package twilio

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// LeadThread stores the Slack channel and thread_ts for a lead phone number.
type LeadThread struct {
	Channel  string `json:"channel"`
	ThreadTS string `json:"thread_ts"`
}

// PhoneThreadStore maps lead phone numbers to Slack threads.
// Keys are stored as-is (E.164). Thread state is persisted to a JSON file.
type PhoneThreadStore struct {
	mu      sync.RWMutex
	threads map[string]LeadThread
	path    string
}

type threadStoreSnapshot struct {
	Threads map[string]LeadThread `json:"threads"`
}

// NewPhoneThreadStore creates a store backed by the given file.
// Pass an empty path for an in-memory-only store (useful in tests).
func NewPhoneThreadStore(path string) *PhoneThreadStore {
	s := &PhoneThreadStore{
		threads: make(map[string]LeadThread),
		path:    path,
	}
	if path != "" {
		s.load()
	}
	return s
}

// GetThread looks up a lead's Slack thread by phone number.
func (s *PhoneThreadStore) GetThread(phone string) (LeadThread, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.threads[phone]
	return t, ok
}

// GetPhone is the reverse of GetThread: returns the phone number for a given
// (channel, threadTS) pair. O(n) scan — safe at Vista Hills lead volumes.
func (s *PhoneThreadStore) GetPhone(channel, threadTS string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for phone, t := range s.threads {
		if t.Channel == channel && t.ThreadTS == threadTS {
			return phone, true
		}
	}
	return "", false
}

// SetThread stores or updates a lead's Slack thread mapping.
func (s *PhoneThreadStore) SetThread(phone string, thread LeadThread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads[phone] = thread
	return s.saveLocked()
}

func (s *PhoneThreadStore) saveLocked() error {
	if s.path == "" {
		return nil
	}
	snap := threadStoreSnapshot{Threads: s.threads}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("thread_store: marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("thread_store: mkdir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("thread_store: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("thread_store: rename: %w", err)
	}
	return nil
}

func (s *PhoneThreadStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var snap threadStoreSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	if snap.Threads != nil {
		s.threads = snap.Threads
	}
}
