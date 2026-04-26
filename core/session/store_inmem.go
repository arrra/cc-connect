package session

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// InMemorySessionStore implements SessionStore using an in-memory map.
// All four methods are guarded by a single coarse mutex — intentional for v1.
// Per-key locking and RW separation are v2 improvements.
type InMemorySessionStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	// savedPins holds pins loaded from disk at startup (keyed by session_key).
	// New sessions for a key inherit these pins. Pins from terminated sessions
	// are merged here so they survive the next Spawn call.
	savedPins map[string][]PinnedItem
	// pinStore is optional; when non-nil, Terminate and SavePins flush to disk.
	pinStore *PinStore
}

// NewInMemorySessionStore returns an empty store with the given pre-loaded pins.
// Pass nil for savedPins when no saved pins are available.
// Pass nil for pinStore to disable disk persistence (tests, flag-off path).
func NewInMemorySessionStore(pinStore *PinStore, savedPins map[string][]PinnedItem) *InMemorySessionStore {
	if savedPins == nil {
		savedPins = make(map[string][]PinnedItem)
	}
	return &InMemorySessionStore{
		sessions:  make(map[string]*Session),
		savedPins: savedPins,
		pinStore:  pinStore,
	}
}

// Spawn creates a new session for sessionKey.
// Any previously saved pins for sessionKey are pre-loaded into the new session.
func (s *InMemorySessionStore) Spawn(sessionKey, rootObjective string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	pinned := make([]PinnedItem, len(s.savedPins[sessionKey]))
	copy(pinned, s.savedPins[sessionKey])

	sess := &Session{
		SessionID:      uuid.New().String(),
		SessionKey:     sessionKey,
		RootObjective:  rootObjective,
		Pinned:         pinned,
		TurnCount:      0,
		LastActivityTs: now,
		CreatedAt:      now,
	}
	s.sessions[sessionKey] = sess
	return sess, nil
}

// GetByKey returns the session for sessionKey, or nil if none exists.
func (s *InMemorySessionStore) GetByKey(sessionKey string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionKey]
	if !ok {
		return nil, nil
	}
	return sess, nil
}

// Update replaces the stored session with the caller-supplied value.
// Returns an error if no session exists for the session's key.
func (s *InMemorySessionStore) Update(sess *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.sessions[sess.SessionKey]; !ok {
		return fmt.Errorf("session not found for key %q", sess.SessionKey)
	}
	s.sessions[sess.SessionKey] = sess
	return nil
}

// Terminate removes the session for sessionKey, merges its pins into savedPins,
// and flushes all pins to disk (if a PinStore was provided).
func (s *InMemorySessionStore) Terminate(sessionKey string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionKey]
	if !ok {
		return fmt.Errorf("session not found for key %q", sessionKey)
	}
	if len(sess.Pinned) > 0 {
		s.savedPins[sessionKey] = append([]PinnedItem(nil), sess.Pinned...)
	}
	delete(s.sessions, sessionKey)

	if s.pinStore != nil {
		return s.pinStore.Save(s.savedPins)
	}
	return nil
}

// SavePins flushes all current pins — from both active sessions and savedPins —
// to disk. Called by the /pin handler after adding a pin to an active session.
func (s *InMemorySessionStore) SavePins() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pinStore == nil {
		return nil
	}
	return s.pinStore.Save(s.collectAllPinsLocked())
}

// collectAllPinsLocked builds a unified map of all pins across savedPins and
// currently active sessions. Must be called with s.mu held.
func (s *InMemorySessionStore) collectAllPinsLocked() map[string][]PinnedItem {
	all := make(map[string][]PinnedItem)
	for k, v := range s.savedPins {
		all[k] = append([]PinnedItem(nil), v...)
	}
	for k, sess := range s.sessions {
		if len(sess.Pinned) > 0 {
			all[k] = append([]PinnedItem(nil), sess.Pinned...)
		}
	}
	return all
}
