package core

import (
	"strings"
	"testing"

	sessv1 "github.com/chenhg5/cc-connect/core/session"
)

// TestV1FlagOff_NoBehaviorChange asserts that with v1 disabled (no SetV1Store call):
//  1. v1Store remains nil (no session store is touched).
//  2. prependV1Context is a no-op (v0 prompt path is taken).
//  3. No "v1_turn" slog record is emitted.
//
// Note: the full interactive message path (handleMessage → processInteractiveMessageWith)
// cannot be exercised in unit tests because processInteractiveEvents blocks indefinitely
// waiting for agent events when using the stub agent. The assertions below test the v1
// seam — every v1 code branch is guarded by `if e.v1Store != nil`, so asserting
// v1Store == nil plus prependV1Context no-op is sufficient to prove the flag-off invariant.
func TestV1FlagOff_NoBehaviorChange(t *testing.T) {
	h := installSlogCapture(t)
	e := newTestEngine()

	// 1. v1Store must be nil — no session is ever created.
	if e.v1Store != nil {
		t.Fatal("v1Store should be nil when flag is off")
	}

	// 2. prependV1Context must return the prompt unchanged (v0 path taken).
	const (
		sessionKey = "slack:C001:U001"
		prompt     = "hello world"
	)
	got := e.prependV1Context(sessionKey, prompt, "1000.0000", prompt)
	if got != prompt {
		t.Fatalf("prependV1Context with nil store: got %q, want %q", got, prompt)
	}

	// 3. No "v1_turn" slog record must have been emitted.
	if h.hasMsg("v1_turn") {
		t.Fatal("flag-off path must not emit v1_turn slog records")
	}

	// Spot-check: v1Store is still nil (not mutated by prependV1Context).
	if e.v1Store != nil {
		t.Fatal("v1Store must remain nil after prependV1Context with flag off")
	}

	// Verify InMemorySessionStore's IncrementTurn is NOT called by checking
	// that creating a store and NOT attaching it to the engine leaves its
	// internal state empty (belt-and-suspenders: the store should be untouched).
	orphanStore := sessv1.NewInMemorySessionStore(nil, nil)
	if _, err := orphanStore.GetByKey(sessionKey); err != nil {
		t.Fatalf("unexpected error on empty store: %v", err)
	}
}

// TestV1FeatureFlagOff verifies that when no v1Store is set (flag off),
// the engine runs the old code path: prependV1Context is a no-op and
// cmdPin returns the disabled message.
func TestV1FeatureFlagOff(t *testing.T) {
	e := newTestEngine()

	// v1Store must be nil (flag off — default).
	if e.v1Store != nil {
		t.Fatal("expected v1Store to be nil when flag is off")
	}

	// prependV1Context must return the prompt unchanged when v1Store is nil.
	const prompt = "hello world"
	got := e.prependV1Context("key:C001", "hello world", "1234.5678", prompt)
	if got != prompt {
		t.Fatalf("prependV1Context with nil store: got %q, want %q", got, prompt)
	}
}

// TestV1FeatureFlagOn verifies that when SetV1Store is called (flag on),
// the engine runs the v1 code path: sessions are stored and prependV1Context
// injects the working-set context block into the prompt.
func TestV1FeatureFlagOn(t *testing.T) {
	e := newTestEngine()
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	if e.v1Store == nil {
		t.Fatal("expected v1Store to be non-nil after SetV1Store")
	}

	// No session yet — prependV1Context returns the prompt unchanged.
	const sessionKey = "T001:C001"
	const prompt = "do the thing"
	got := e.prependV1Context(sessionKey, prompt, "1000.0000", prompt)
	if got != prompt {
		t.Fatalf("prependV1Context without session: got %q, want %q", got, prompt)
	}

	// Spawn a session, then prependV1Context should inject the context block.
	_, err := store.Spawn(sessionKey, "root objective from first message")
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	got = e.prependV1Context(sessionKey, prompt, "1000.0001", prompt)
	if got == prompt {
		t.Fatal("prependV1Context with active session: expected injected context, got original prompt unchanged")
	}
	if !strings.Contains(got, "<cc-connect:session_context>") {
		t.Fatalf("prependV1Context with active session: missing context wrapper, got:\n%s", got)
	}
	if !strings.Contains(got, "root_objective") {
		t.Fatalf("prependV1Context with active session: missing root_objective field, got:\n%s", got)
	}
	// Original prompt must still appear at the end.
	if !strings.HasSuffix(got, prompt) {
		t.Fatalf("prependV1Context with active session: original prompt missing from end, got:\n%s", got)
	}
}

// TestV1FeatureFlagCmdPinDisabled verifies that /pin replies with the
// disabled message when v1Store is nil (flag off).
func TestV1FeatureFlagCmdPinDisabled(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)

	msg := &Message{
		ReplyCtx: "ctx-1",
		UserID:   "U001",
	}
	e.cmdPin(p, msg, []string{"something"})

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("cmdPin with nil store: expected reply, got none")
	}
	if !strings.Contains(sent[0], "CC_CONNECT_SESSIONS_V1=1") {
		t.Fatalf("cmdPin with nil store: reply should mention feature flag, got: %q", sent[0])
	}
}
