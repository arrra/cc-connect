package core

import (
	"strings"
	"testing"
)

// TestV1Wiring_AtMentionSpawnsV1Session mirrors the live-smoke bug: with the v1
// flag on, an @mention should create a v1 session so that a subsequent /pin
// succeeds. Before the fix, handleMessage never called Spawn, so cmdPin
// always returned "No active session for this thread."
//
// Note: handleMessage launches processInteractiveMessageWith in a goroutine
// (engine.go:1628) that blocks indefinitely with the stub agent. The v1 spawn
// at engine.go:1569 runs synchronously before that goroutine starts, so we can
// assert store state as soon as handleMessage returns.
func TestV1Wiring_AtMentionSpawnsV1Session(t *testing.T) {
	e, store, p := newV1TestEngine(t, true)

	const sessionKey = "slack:C123:1234.5678"
	msg := synthesizeMsg(sessionKey, "hello bot, please help", "U001", "ctx-1", "")

	e.handleMessage(p, msg)

	// Assert: v1 store has a session under the exact session_key.
	sess, err := store.GetByKey(sessionKey)
	if err != nil {
		t.Fatalf("GetByKey after @mention: %v", err)
	}
	if sess == nil {
		t.Fatalf("v1 store: no session for %q after @mention — handleMessage spawn-or-attach not wired", sessionKey)
	}

	// Assert: cmdPin with the SAME session_key succeeds (no "No active session" error).
	p.clearSent()
	pinMsg := synthesizeMsg(sessionKey, "/pin regression test pin", "U001", "ctx-1", "")
	e.cmdPin(p, pinMsg, []string{"regression test pin"})

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("cmdPin: expected a reply, got none")
	}
	if strings.Contains(sent[0], "No active session") {
		t.Fatalf("cmdPin: got 'No active session' — spawn-or-attach in handleMessage not effective; reply: %q", sent[0])
	}
	if !strings.Contains(sent[0], "Pinned") {
		t.Fatalf("cmdPin: expected confirmation reply, got: %q", sent[0])
	}

	// Assert: pin persisted in the store.
	sess, err = store.GetByKey(sessionKey)
	if err != nil {
		t.Fatalf("GetByKey after cmdPin: %v", err)
	}
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin in store, got %d", len(sess.Pinned))
	}
	if sess.Pinned[0].Text != "regression test pin" {
		t.Errorf("pin text = %q, want %q", sess.Pinned[0].Text, "regression test pin")
	}
}

// TestV1Wiring_FlagOff_NoV1Session locks the invariant that with the flag off,
// handleMessage must not touch any v1 store (v1Store is nil, so the spawn-or-attach
// code is skipped entirely).
func TestV1Wiring_FlagOff_NoV1Session(t *testing.T) {
	e, _, p := newV1TestEngine(t, false) // flag off — store is nil

	if e.v1Store != nil {
		t.Fatal("v1Store should be nil when flag is off")
	}

	const sessionKey = "slack:C123:1234.5678"
	msg := synthesizeMsg(sessionKey, "hello bot, please help", "U001", "ctx-1", "")

	// handleMessage must not mutate v1Store when flag is off (guarded by v1Store != nil).
	e.handleMessage(p, msg)

	if e.v1Store != nil {
		t.Fatal("handleMessage must not set v1Store when flag is off")
	}
}
