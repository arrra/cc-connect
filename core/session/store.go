package session

// SessionStore manages the lifecycle of sessions.
type SessionStore interface {
	// Spawn creates a new session for the given sessionKey with a fresh UUID.
	// If saved pins exist for sessionKey, they are loaded into the new session.
	Spawn(sessionKey, rootObjective string) (*Session, error)
	// GetByKey returns the active session for sessionKey, or nil if none exists.
	GetByKey(sessionKey string) (*Session, error)
	// Update persists changes to an existing session (e.g. updated TurnCount, WorkingSet).
	Update(s *Session) error
	// Terminate removes the session for sessionKey and writes its pins to disk.
	Terminate(sessionKey string) error
	// SavePins flushes all current pin state to disk.
	// Called by /pin after adding a pin to an active session.
	SavePins() error
}
