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

// SpawnOrAttach atomically returns the existing session for sessionKey, or spawns a
// new one with rootObjective if none exists. Holds the mutex for the full
// read-modify-write to close the concurrent-spawn race. Returns (session, wasSpawned, err).
func (s *InMemorySessionStore) SpawnOrAttach(sessionKey, rootObjective string) (*Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.sessions[sessionKey]; ok {
		snapshot := *existing
		return &snapshot, false, nil
	}

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
	result := *sess
	return &result, true, nil
}

// GetByKey returns the session for sessionKey, or nil if none exists.
// READ-ONLY — do not mutate the returned pointer. Use IncrementTurn, AddPin,
// or SetWorkingSet for operations that modify session state.
func (s *InMemorySessionStore) GetByKey(sessionKey string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionKey]
	if !ok {
		return nil, nil
	}
	return sess, nil
}

// IncrementTurn atomically increments TurnCount and sets LastActivityTs = now.
// Returns a shallow copy of the updated session, or (nil, nil) if no session exists.
func (s *InMemorySessionStore) IncrementTurn(sessionKey string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionKey]
	if !ok {
		return nil, nil
	}
	sess.TurnCount++
	sess.LastActivityTs = time.Now()
	copy := *sess
	return &copy, nil
}

// AddPin atomically appends pin to the session's Pinned slice.
// Returns ErrPinLimitReached if the session already holds MaxPinsPerSession pins.
// Returns a shallow copy of the updated session, or (nil, nil) if no session exists.
// AddPin appends pin to sessionKey's session.
// Returns ErrSessionNotFound if no session exists for sessionKey.
// Returns ErrPinLimitReached if the session already holds MaxPinsPerSession pins.
func (s *InMemorySessionStore) AddPin(sessionKey string, pin PinnedItem) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionKey]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if len(sess.Pinned) >= MaxPinsPerSession {
		return nil, ErrPinLimitReached
	}
	sess.Pinned = append(sess.Pinned, pin)
	copy := *sess
	return &copy, nil
}

// RemovePin removes the pin at 0-based idx from the session's Pinned slice.
// Returns ErrSessionNotFound if no session exists, ErrPinNotFound if idx is out of range.
// SavePins must be called by the caller after this returns to persist the change.
func (s *InMemorySessionStore) RemovePin(sessionKey string, idx int) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionKey]
	if !ok {
		return nil, ErrSessionNotFound
	}
	if idx < 0 || idx >= len(sess.Pinned) {
		return nil, ErrPinNotFound
	}
	sess.Pinned = append(sess.Pinned[:idx], sess.Pinned[idx+1:]...)
	copy := *sess
	return &copy, nil
}

// ResetScope clears the session's Pinned slice and RootObjective without terminating it.
// Returns ErrSessionNotFound if no session exists.
// SavePins must be called by the caller after this returns to persist the change.
func (s *InMemorySessionStore) ResetScope(sessionKey string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionKey]
	if !ok {
		return nil, ErrSessionNotFound
	}
	sess.RootObjective = ""
	sess.Pinned = nil
	copy := *sess
	return &copy, nil
}

// SetWorkingSet atomically replaces the session's WorkingSet.
// Returns a shallow copy of the updated session, or (nil, nil) if no session exists.
func (s *InMemorySessionStore) SetWorkingSet(sessionKey string, ws *WorkingSet) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionKey]
	if !ok {
		return nil, nil
	}
	if ws != nil {
		sess.WorkingSet = *ws
	}
	copy := *sess
	return &copy, nil
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

// AppendTurn records a TurnSnapshot to the session's TurnHistory, capped at MaxTurnHistory.
func (s *InMemorySessionStore) AppendTurn(sessionKey string, snap TurnSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[sessionKey]
	if !ok {
		return ErrSessionNotFound
	}
	sess.TurnHistory = append(sess.TurnHistory, snap)
	if len(sess.TurnHistory) > MaxTurnHistory {
		sess.TurnHistory = sess.TurnHistory[len(sess.TurnHistory)-MaxTurnHistory:]
	}
	return nil
}

// GetPinnedByKey returns a defensive copy of the pins for the given key.
// Active sessions take precedence over savedPins. Returns nil, nil if neither exists.
func (s *InMemorySessionStore) GetPinnedByKey(key string) ([]PinnedItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[key]; ok {
		if len(sess.Pinned) == 0 {
			return nil, nil
		}
		result := make([]PinnedItem, len(sess.Pinned))
		copy(result, sess.Pinned)
		return result, nil
	}
	if saved, ok := s.savedPins[key]; ok && len(saved) > 0 {
		result := make([]PinnedItem, len(saved))
		copy(result, saved)
		return result, nil
	}
	return nil, nil
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
