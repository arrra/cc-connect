package twilio

import (
	"encoding/json"
	"fmt"
	"log/slog"
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
// The write lock is held through persist so concurrent callers cannot write
// the same .tmp file and race on rename.
func (s *PhoneThreadStore) SetThread(phone string, thread LeadThread) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.threads[phone] = thread
	snap := s.snapshotLocked()
	return s.persist(snap)
}

// snapshotLocked returns a deep copy of the current thread map. Caller must hold s.mu.
func (s *PhoneThreadStore) snapshotLocked() map[string]LeadThread {
	cp := make(map[string]LeadThread, len(s.threads))
	for k, v := range s.threads {
		cp[k] = v
	}
	return cp
}

// persist marshals the snapshot to disk outside of any lock.
func (s *PhoneThreadStore) persist(snap map[string]LeadThread) error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(threadStoreSnapshot{Threads: snap}, "", "  ")
	if err != nil {
		return fmt.Errorf("thread_store: marshal: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("thread_store: mkdir: %w", err)
	}
	// Ensure directory is 0700 regardless of umask.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("thread_store: chmod dir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("thread_store: write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("thread_store: rename: %w", err)
	}
	// Explicit chmod after rename; rename preserves the source perms but umask
	// could have widened them if the tmp write used a different umask path.
	if err := os.Chmod(s.path, 0o600); err != nil {
		return fmt.Errorf("thread_store: chmod file: %w", err)
	}
	return nil
}

func (s *PhoneThreadStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	// Fix legacy files that may have been created world-readable (0644).
	if chmodErr := os.Chmod(s.path, 0o600); chmodErr != nil && !os.IsNotExist(chmodErr) {
		slog.Warn("thread_store: could not fix file permissions", "path", s.path, "error", chmodErr)
	}
	// Fix directory permissions if the path has a directory component.
	if dir := filepath.Dir(s.path); dir != "." {
		if di, statErr := os.Stat(dir); statErr == nil && di.Mode().Perm() != 0o700 {
			if chmodErr := os.Chmod(dir, 0o700); chmodErr != nil {
				slog.Warn("thread_store: could not fix dir permissions", "path", dir, "error", chmodErr)
			}
		}
	}
	var snap threadStoreSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return
	}
	if snap.Threads != nil {
		s.threads = snap.Threads
	}
}
