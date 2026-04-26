package core

import (
	"fmt"
	"strings"
	"testing"

	sessv1 "github.com/chenhg5/cc-connect/core/session"
)

// TestCmdPin_ReplyTo verifies that /pin with empty args and a ParentText in the
// message context pins the parent message text with pinned_via="reply_to".
func TestCmdPin_ReplyTo(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C001:U001"
	if _, err := store.Spawn(key, "root objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     "U001",
		ParentText: "remember this context",
	}
	e.cmdPin(p, msg, []string{}) // empty args — reply-to path

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "Pinned") {
		t.Errorf("expected confirmation reply, got: %q", sent[0])
	}
	if !strings.Contains(sent[0], "remember this context") {
		t.Errorf("reply should include pinned text, got: %q", sent[0])
	}

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(sess.Pinned))
	}
	if sess.Pinned[0].Text != "remember this context" {
		t.Errorf("pin text = %q, want %q", sess.Pinned[0].Text, "remember this context")
	}
	if sess.Pinned[0].PinnedVia != "reply_to" {
		t.Errorf("pinned_via = %q, want %q", sess.Pinned[0].PinnedVia, "reply_to")
	}
}

// TestCmdPin_NoArgsNoParent verifies that /pin with empty args and no ParentText
// returns the updated usage error that mentions the reply-to path.
func TestCmdPin_NoArgsNoParent(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	msg := &Message{
		SessionKey: "slack:C001:U001",
		ReplyCtx:   "ctx",
		UserID:     "U001",
		// ParentText intentionally empty
	}
	e.cmdPin(p, msg, []string{})

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected an error reply, got none")
	}
	if !strings.Contains(sent[0], "reply to a message") {
		t.Errorf("error should mention reply-to path, got: %q", sent[0])
	}
	if !strings.Contains(sent[0], "Usage") {
		t.Errorf("error should include usage hint, got: %q", sent[0])
	}
}

// TestCmdPin_PinLimitReached verifies that cmdPin replies with the limit-reached
// message when the session already holds MaxPinsPerSession pins.
func TestCmdPin_PinLimitReached(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C001:U003"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     "U003",
	}
	// Fill to the cap.
	for i := range sessv1.MaxPinsPerSession {
		p.clearSent()
		e.cmdPin(p, msg, []string{fmt.Sprintf("pin-%d", i)})
	}

	// One more must trigger the limit reply.
	p.clearSent()
	e.cmdPin(p, msg, []string{"one too many"})

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a limit-reached reply, got none")
	}
	if !strings.Contains(sent[0], "Pin limit reached") {
		t.Errorf("expected pin-limit message, got: %q", sent[0])
	}
}

// TestCmdPin_TextArg_PinnedViaEmpty verifies that /pin <text> (the existing path)
// leaves PinnedVia empty (no pinned_via field in JSON output).
func TestCmdPin_TextArg_PinnedViaEmpty(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C001:U002"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     "U002",
	}
	e.cmdPin(p, msg, []string{"explicit", "text", "pin"})

	sess, _ := store.GetByKey(key)
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(sess.Pinned))
	}
	if sess.Pinned[0].PinnedVia != "" {
		t.Errorf("text-arg pin should have empty pinned_via, got: %q", sess.Pinned[0].PinnedVia)
	}
	if sess.Pinned[0].Text != "explicit text pin" {
		t.Errorf("pin text = %q, want %q", sess.Pinned[0].Text, "explicit text pin")
	}
}
