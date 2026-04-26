package session

// SessionStore manages the lifecycle of sessions.
type SessionStore interface {
	// Spawn creates a new session for the given sessionKey with a fresh UUID.
	// If saved pins exist for sessionKey, they are loaded into the new session.
	Spawn(sessionKey, rootObjective string) (*Session, error)
	// GetByKey returns the active session for sessionKey, or nil if none exists.
	// READ-ONLY — do not mutate the returned pointer. Use IncrementTurn, AddPin,
	// or SetWorkingSet for operations that modify session state.
	GetByKey(sessionKey string) (*Session, error)
	// Update persists changes to an existing session (e.g. updated TurnCount, WorkingSet).
	Update(s *Session) error
	// Terminate removes the session for sessionKey and writes its pins to disk.
	Terminate(sessionKey string) error
	// SavePins flushes all current pin state to disk.
	// Called by /pin after adding a pin to an active session.
	SavePins() error
	// IncrementTurn atomically increments TurnCount and sets LastActivityTs = now.
	// Returns a shallow copy of the updated session, or (nil, nil) if no session exists.
	IncrementTurn(sessionKey string) (*Session, error)
	// AddPin atomically appends pin to the session's Pinned slice.
	// Returns a shallow copy of the updated session, or (nil, nil) if no session exists.
	AddPin(sessionKey string, pin PinnedItem) (*Session, error)
	// SetWorkingSet atomically replaces the session's WorkingSet.
	// Returns a shallow copy of the updated session, or (nil, nil) if no session exists.
	SetWorkingSet(sessionKey string, ws *WorkingSet) (*Session, error)
}
