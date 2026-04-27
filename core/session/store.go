package session

// SessionStore manages the lifecycle of sessions.
type SessionStore interface {
	// Spawn creates a new session for the given sessionKey with a fresh UUID.
	// If saved pins exist for sessionKey, they are loaded into the new session.
	Spawn(sessionKey, rootObjective string) (*Session, error)
	// SpawnOrAttach atomically: if no session exists for sessionKey, spawn one with the
	// given root objective; otherwise return the existing session. Returns the session,
	// a bool indicating whether a new session was created, and any error.
	// Holds the implementation's lock for the full read-modify-write.
	SpawnOrAttach(sessionKey, rootObjective string) (*Session, bool, error)
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
	// RemovePin removes the pin at 0-based idx from the session's Pinned slice.
	// Returns ErrSessionNotFound if no session exists, ErrPinNotFound if idx is out of range.
	RemovePin(sessionKey string, idx int) (*Session, error)
	// ResetScope clears Pinned and RootObjective without terminating the session.
	// Returns ErrSessionNotFound if no session exists.
	ResetScope(sessionKey string) (*Session, error)
	// GetPinnedByKey returns a defensive copy of the pins for the given session key.
	// Returns nil, nil if no session or saved pins exist for that key.
	GetPinnedByKey(key string) ([]PinnedItem, error)
	// AppendTurn records a TurnSnapshot to the session's TurnHistory, capped at MaxTurnHistory.
	// Returns ErrSessionNotFound if no session exists for sessionKey.
	AppendTurn(sessionKey string, snap TurnSnapshot) error
	// IncrementCorrectionCount increments Session.CorrectionCount for sessionKey.
	// Returns ErrSessionNotFound if the session does not exist.
	IncrementCorrectionCount(sessionKey string) (*Session, error)
}
