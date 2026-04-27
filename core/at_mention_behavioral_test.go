package core

import (
	"context"
	"testing"
	"time"

	sessv1 "github.com/chenhg5/cc-connect/core/session"
)

// closingStubAgentSession wraps stubAgentSession. Events() returns the same
// channel each time. Send() puts a single EventResult into the buffered channel
// so processInteractiveEvents exits cleanly via the EventResult path (line 3223)
// rather than channelClosed — avoiding the data race on state.agentSession that
// channelClosed introduces by calling cleanupInteractiveState concurrently with
// the Send goroutine.
type closingStubAgentSession struct {
	stubAgentSession
	events chan Event
}

func newClosingStubAgentSession() *closingStubAgentSession {
	return &closingStubAgentSession{
		events: make(chan Event, 1), // buffered: Send() puts EventResult without blocking
	}
}

func (s *closingStubAgentSession) Events() <-chan Event {
	return s.events
}

func (s *closingStubAgentSession) Send(_ string, _ []ImageAttachment, _ []FileAttachment) error {
	select {
	case s.events <- Event{Type: EventResult}:
	default: // already signalled (e.g. drainEvents consumed it)
	}
	return nil
}

// closingStubAgent creates closingStubAgentSessions so goroutines launched by
// processInteractiveMessageWith complete and post-turn v1 hooks fire.
type closingStubAgent struct{}

func (a *closingStubAgent) Name() string { return "closing-stub" }
func (a *closingStubAgent) StartSession(_ context.Context, _ string) (AgentSession, error) {
	return newClosingStubAgentSession(), nil
}
func (a *closingStubAgent) ListSessions(_ context.Context) ([]AgentSessionInfo, error) {
	return nil, nil
}
func (a *closingStubAgent) Stop() error { return nil }

// appendSignalStore wraps a SessionStore and signals a channel whenever
// AppendTurn is called. Allows tests to synchronize with the async goroutine
// without polling a live pointer (which would race with concurrent writes).
type appendSignalStore struct {
	sessv1.SessionStore
	done chan struct{}
}

func newAppendSignalStore(inner sessv1.SessionStore) *appendSignalStore {
	return &appendSignalStore{
		SessionStore: inner,
		done:         make(chan struct{}, 1),
	}
}

func (s *appendSignalStore) AppendTurn(sessionKey string, snap sessv1.TurnSnapshot) error {
	err := s.SessionStore.AppendTurn(sessionKey, snap)
	select {
	case s.done <- struct{}{}:
	default:
	}
	return err
}

// waitForTurn blocks until AppendTurn fires or the timeout elapses.
func (s *appendSignalStore) waitForTurn(t testing.TB, timeout time.Duration) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(timeout):
		t.Fatalf("AppendTurn did not fire within %s — processInteractiveMessageWith goroutine did not complete", timeout)
	}
}

// newClosingV1TestEngine creates an Engine that uses closingStubAgent (so
// processInteractiveMessageWith goroutines complete) and optionally wraps the
// session store with appendSignalStore for race-free turn-completion detection.
func newClosingV1TestEngine(t testing.TB, flagOn bool) (*Engine, *appendSignalStore, *stubPlatformEngine) {
	t.Helper()
	p := &stubPlatformEngine{n: "v1-closing-test"}
	e := NewEngine("v1-test", &closingStubAgent{}, []Platform{p}, "", LangEnglish)
	var store *appendSignalStore
	if flagOn {
		inner := sessv1.NewInMemorySessionStore(nil, nil)
		store = newAppendSignalStore(inner)
		e.SetV1Store(store)
	}
	return e, store, p
}

// TestAtMention_Behavioral_SpawnsAndAppendsTurn exercises the full production
// handleMessage path for an @mention and asserts:
//  1. v1Store has a session with RootObjective == "verify v1 spawn"
//  2. AppendTurn recorded 1 turn (TurnHistory len == 1)
//  3. slog emitted "v1_turn" and "v1_meter" records
//
// This test would have caught q-065 (Router never wired into handleMessage):
// the v1Store session assertion fails when SpawnOrAttach is never called, and
// the AppendTurn wait times out when the post-turn hook is absent.
func TestAtMention_Behavioral_SpawnsAndAppendsTurn(t *testing.T) {
	logs := installSlogCapture(t)
	e, store, p := newClosingV1TestEngine(t, true)

	const sessionKey = "slack:C123"
	msg := synthesizeMsg(sessionKey, "<@UBOTID> verify v1 spawn", "U001", "ctx-1", "")

	e.handleMessage(p, msg)

	// SpawnOrAttach is synchronous — session exists immediately after handleMessage.
	sess, err := store.GetByKey(sessionKey)
	if err != nil {
		t.Fatalf("store.GetByKey(%q) after handleMessage: %v", sessionKey, err)
	}
	if sess == nil {
		t.Fatalf("v1 session not created for %q — SpawnOrAttach not wired in handleMessage", sessionKey)
	}
	if sess.RootObjective != "verify v1 spawn" {
		t.Errorf("RootObjective = %q, want %q", sess.RootObjective, "verify v1 spawn")
	}

	// AppendTurn fires async inside the processInteractiveMessageWith goroutine.
	// waitForTurn uses a channel (not polling a live pointer) to avoid data races.
	store.waitForTurn(t, 3*time.Second)

	// GetByKey after waitForTurn: AppendTurn has completed, no concurrent writes.
	sess, err = store.GetByKey(sessionKey)
	if err != nil {
		t.Fatalf("store.GetByKey(%q) after AppendTurn: %v", sessionKey, err)
	}
	if len(sess.TurnHistory) != 1 {
		t.Errorf("TurnHistory len = %d, want 1 — AppendTurn did not record the turn", len(sess.TurnHistory))
	}

	// EmitTurnLog and EmitMeterLog are called sequentially after AppendTurn in
	// the same goroutine. Brief wait to let those slog calls land.
	time.Sleep(10 * time.Millisecond)

	if !logs.hasMsg("v1_turn") {
		t.Error("slog: expected 'v1_turn' record — EmitTurnLog not called")
	}
	if !logs.hasMsg("v1_meter") {
		t.Error("slog: expected 'v1_meter' record — EmitMeterLog not called")
	}
}

// TestAtMention_v1FlagOff_NoV1SideEffects locks the invariant that with the
// v1 flag off (v1Store nil), handleMessage must not touch any v1 state and
// must not emit v1_turn or v1_meter slog records.
//
// This test would have caught q-065: if handleMessage called the Router which
// unconditionally set up a v1 session, the v1Store nil guard would panic.
func TestAtMention_v1FlagOff_NoV1SideEffects(t *testing.T) {
	logs := installSlogCapture(t)
	e, _, p := newClosingV1TestEngine(t, false) // flag off — store is nil

	if e.v1Store != nil {
		t.Fatal("precondition: v1Store should be nil when flag is off")
	}

	const sessionKey = "slack:C123"
	msg := synthesizeMsg(sessionKey, "<@UBOTID> verify v1 spawn", "U001", "ctx-1", "")

	e.handleMessage(p, msg)

	// Wait for the goroutine to finish: EventResult causes quick completion.
	time.Sleep(100 * time.Millisecond)

	if e.v1Store != nil {
		t.Fatal("v1Store must remain nil after handleMessage when flag is off")
	}
	if logs.hasMsg("v1_turn") {
		t.Error("slog: unexpected 'v1_turn' record — v1 path must not fire when flag is off")
	}
	if logs.hasMsg("v1_meter") {
		t.Error("slog: unexpected 'v1_meter' record — v1 path must not fire when flag is off")
	}
}
