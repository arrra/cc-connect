package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

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

// TestCmdPin_NoArgsNoParent_ReturnsClearError (t-5) verifies that /pin with empty
// text AND no ParentText (slash command from main channel, not a thread reply):
//  1. creates no pin in the store
//  2. replies with the exact user-facing error string (locked against regression)
//  3. does not attempt any Slack API call — at the engine level this is observable
//     because AddPin is never reached (the code returns before it)
//
// Note: conversations.replies lives in the Slack platform layer (platform/slack/slack.go).
// Verifying that it was NOT called requires a live Slack smoke test, not an engine test.
// Engine-level proof is: the session has 0 pins after the call.
func TestCmdPin_NoArgsNoParent_ReturnsClearError(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C099:U099"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     "U099",
		// ParentText intentionally empty — simulates main-channel slash command
	}
	e.cmdPin(p, msg, []string{}) // no args, no parent text

	// 1. No pin must be created.
	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != 0 {
		t.Fatalf("expected 0 pins, got %d", len(sess.Pinned))
	}

	// 2. Exact user-facing error string (locked against regression — read from source).
	const wantErr = "Usage: /pin <text> OR reply to a message and run /pin"
	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected an error reply, got none")
	}
	if sent[0] != wantErr {
		t.Errorf("error reply = %q, want %q", sent[0], wantErr)
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

// TestCmdPin_CapEnforced_AtMax (t-6) verifies the full cap-enforcement path:
//  1. First MaxPinsPerSession pins all succeed and are in the store.
//  2. The (MaxPinsPerSession+1)th attempt triggers the cap: Slack reply contains the
//     limit-reached message.
//  3. Pin store still has exactly MaxPinsPerSession items after the failed attempt.
func TestCmdPin_CapEnforced_AtMax(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C200:U200"
	if _, err := store.Spawn(key, "cap test objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     "U200",
	}

	// 1. Add exactly MaxPinsPerSession pins — all must succeed.
	for i := range sessv1.MaxPinsPerSession {
		p.clearSent()
		e.cmdPin(p, msg, []string{fmt.Sprintf("cap-pin-%d", i)})
		sent := p.getSent()
		if len(sent) == 0 {
			t.Fatalf("pin %d: expected success reply, got none", i)
		}
		if strings.Contains(sent[0], "Pin limit reached") {
			t.Fatalf("pin %d (within cap): got limit-reached message prematurely: %q", i, sent[0])
		}
	}

	// Verify store has exactly MaxPinsPerSession items.
	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey after filling cap: %v", err)
	}
	if len(sess.Pinned) != sessv1.MaxPinsPerSession {
		t.Fatalf("after %d pins: store has %d items, want %d", sessv1.MaxPinsPerSession, len(sess.Pinned), sessv1.MaxPinsPerSession)
	}

	// 2. The (cap+1)th pin must be rejected with the limit-reached message.
	p.clearSent()
	e.cmdPin(p, msg, []string{"one too many"})
	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("cap+1 pin: expected limit-reached reply, got none")
	}
	const wantMsg = "Pin limit reached"
	if !strings.Contains(sent[0], wantMsg) {
		t.Errorf("cap+1 pin reply = %q, want to contain %q", sent[0], wantMsg)
	}

	// 3. Pin store must still have exactly MaxPinsPerSession items (not MaxPinsPerSession+1).
	sess, err = store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey after cap+1 attempt: %v", err)
	}
	if len(sess.Pinned) != sessv1.MaxPinsPerSession {
		t.Fatalf("after cap+1 attempt: store has %d items, want %d (cap must not be breached)", len(sess.Pinned), sessv1.MaxPinsPerSession)
	}
}

// TestCmdPin_CapEnforced_19thAnd20thBothSucceed (t-6) explicitly asserts that the
// 19th and 20th pins both succeed — an off-by-one regression guard.
// (Tests using >= vs > in the cap check.)
func TestCmdPin_CapEnforced_19thAnd20thBothSucceed(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C201:U201"
	if _, err := store.Spawn(key, "boundary test"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     "U201",
	}

	// Pre-fill to 18 pins (MaxPinsPerSession - 2 = 18).
	for i := range sessv1.MaxPinsPerSession - 2 {
		p.clearSent()
		e.cmdPin(p, msg, []string{fmt.Sprintf("pre-fill-%d", i)})
	}

	// 19th pin must succeed.
	p.clearSent()
	e.cmdPin(p, msg, []string{"pin-number-19"})
	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("19th pin: expected success reply, got none")
	}
	if strings.Contains(sent[0], "Pin limit reached") {
		t.Fatalf("19th pin: unexpected limit-reached message: %q", sent[0])
	}

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey after 19th pin: %v", err)
	}
	if len(sess.Pinned) != sessv1.MaxPinsPerSession-1 {
		t.Fatalf("after 19th pin: store has %d items, want %d", len(sess.Pinned), sessv1.MaxPinsPerSession-1)
	}

	// 20th pin must also succeed — this is the off-by-one regression guard.
	p.clearSent()
	e.cmdPin(p, msg, []string{"pin-number-20"})
	sent = p.getSent()
	if len(sent) == 0 {
		t.Fatal("20th pin: expected success reply, got none")
	}
	if strings.Contains(sent[0], "Pin limit reached") {
		t.Fatalf("20th pin: unexpected limit-reached message (off-by-one?): %q", sent[0])
	}

	sess, err = store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey after 20th pin: %v", err)
	}
	if len(sess.Pinned) != sessv1.MaxPinsPerSession {
		t.Fatalf("after 20th pin: store has %d items, want %d", len(sess.Pinned), sessv1.MaxPinsPerSession)
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

// TestCmdPin_ReplyTo_Threaded_PinsParentText (t-4) verifies the full reply-to /pin path:
//  1. Pin store contains a PinnedItem with text == the parent message.
//  2. pinned_via == "reply_to".
//  3. Slack reply was sent confirming the pin.
//
// Architecture note: conversations.replies is called by the Slack platform layer
// (platform/slack/slack.go:fetchThreadRootText) BEFORE the engine is invoked.
// The platform sets msg.ParentText; cmdPin reads it without any Slack API call.
// The "exactly one conversations.replies call" assertion from the spec cannot be
// verified at the engine level — it requires a live Slack smoke test.
func TestCmdPin_ReplyTo_Threaded_PinsParentText(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const (
		key        = "slack:C010:1700000001.000001"
		userID     = "U010"
		parentText = "the parent message I'm replying to"
	)
	if _, err := store.Spawn(key, "root objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Simulate: Slack platform pre-fetched the thread-root text and set ParentText.
	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     userID,
		ParentText: parentText, // platform layer populates this from conversations.replies
	}
	e.cmdPin(p, msg, []string{}) // empty args → reply-to path

	// 1. Pin store contains PinnedItem with text == parent message.
	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(sess.Pinned))
	}
	if sess.Pinned[0].Text != parentText {
		t.Errorf("pin.Text = %q, want %q", sess.Pinned[0].Text, parentText)
	}

	// 2. pinned_via == "reply_to".
	if sess.Pinned[0].PinnedVia != "reply_to" {
		t.Errorf("pin.PinnedVia = %q, want %q", sess.Pinned[0].PinnedVia, "reply_to")
	}

	// 3. Slack reply was sent confirming the pin.
	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a confirmation reply, got none")
	}
	if !strings.Contains(sent[0], "Pinned") {
		t.Errorf("reply should confirm pin, got: %q", sent[0])
	}
	if !strings.Contains(sent[0], parentText) {
		t.Errorf("reply should include pinned text, got: %q", sent[0])
	}
}

// TestCmdPin_ReplyTo_SlackAPIError (t-4) tests the engine-level analog of a
// conversations.replies failure. When the Slack platform fails to fetch the
// parent message text, it does NOT populate msg.ParentText (leaving it empty).
// cmdPin must surface a clear error to the user and create no pin.
//
// Note: the actual conversations.replies call and its error handling live in
// platform/slack/slack.go:fetchThreadRootText, outside the engine boundary.
// Full error-path coverage requires a live Slack smoke or platform-layer mock.
func TestCmdPin_ReplyTo_SlackAPIError(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C011:1700000002.000002"
	if _, err := store.Spawn(key, "root objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// ParentText empty: simulates what the platform does when conversations.replies fails.
	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     "U011",
		ParentText: "", // no parent text — Slack API error equivalent
	}
	e.cmdPin(p, msg, []string{}) // empty args, empty ParentText

	// No pin must be created.
	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != 0 {
		t.Fatalf("expected 0 pins after error, got %d", len(sess.Pinned))
	}

	// A clear usage error must be surfaced to the user (not silently swallowed).
	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected an error reply, got none")
	}
	if !strings.Contains(sent[0], "Usage") {
		t.Errorf("error reply should include usage hint, got: %q", sent[0])
	}
	if !strings.Contains(sent[0], "reply to a message") {
		t.Errorf("error reply should mention reply-to path, got: %q", sent[0])
	}
}

// TestCmdPin_WithText_PersistsAndInjects (t-3) verifies that /pin <text>:
//  1. persists a PinnedItem with source="user_explicit", non-zero pinned_at, pinned_by=userID
//  2. MarshalSystemContext for the next turn includes the pinned text in pinned[]
//  3. Slack reply confirmation was sent
func TestCmdPin_WithText_PersistsAndInjects(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const (
		key    = "slack:C001:U042"
		userID = "U042"
		pinTxt = "my pinned note"
	)
	if _, err := store.Spawn(key, "root objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{
		SessionKey: key,
		ReplyCtx:   "ctx",
		UserID:     userID,
	}
	e.cmdPin(p, msg, []string{"my", "pinned", "note"})

	// 1. Pin was persisted with correct metadata.
	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin, got %d", len(sess.Pinned))
	}
	pin := sess.Pinned[0]
	if pin.Text != pinTxt {
		t.Errorf("pin.Text = %q, want %q", pin.Text, pinTxt)
	}
	if pin.Source != "user_explicit" {
		t.Errorf("pin.Source = %q, want %q", pin.Source, "user_explicit")
	}
	if pin.PinnedAt.IsZero() {
		t.Error("pin.PinnedAt must be non-zero")
	}
	if pin.PinnedBy != userID {
		t.Errorf("pin.PinnedBy = %q, want %q", pin.PinnedBy, userID)
	}
	// Text-arg path: PinnedVia must be empty (not "text_arg" — no constant for this in v1).
	// TODO: introduce "text_arg" constant in v2 to mirror "reply_to".
	if pin.PinnedVia != "" {
		t.Errorf("text-arg pin.PinnedVia = %q, want empty", pin.PinnedVia)
	}

	// 2. MarshalSystemContext for the next turn includes the pinned text.
	ws := sessv1.BuildWorkingSet(sess, &sessv1.UserMessage{Text: "next turn", Ts: "1000.0001"})
	ctxJSON, err := sessv1.MarshalSystemContext(ws)
	if err != nil {
		t.Fatalf("MarshalSystemContext: %v", err)
	}
	var out struct {
		Pinned []struct {
			Text string `json:"text"`
		} `json:"pinned"`
	}
	if err := json.Unmarshal([]byte(ctxJSON), &out); err != nil {
		t.Fatalf("unmarshal context JSON: %v", err)
	}
	if len(out.Pinned) != 1 {
		t.Fatalf("context JSON pinned[] has %d items, want 1", len(out.Pinned))
	}
	if out.Pinned[0].Text != pinTxt {
		t.Errorf("context JSON pinned[0].text = %q, want %q", out.Pinned[0].Text, pinTxt)
	}

	// 3. Slack reply confirmation was sent.
	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "Pinned") {
		t.Errorf("reply should confirm pin, got: %q", sent[0])
	}
	if !strings.Contains(sent[0], pinTxt) {
		t.Errorf("reply should include pinned text, got: %q", sent[0])
	}
}

// ── /context tests ──────────────────────────────────────────────────────────

func TestCmdContext_NoSession(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	msg := &Message{SessionKey: "slack:C900:U900", ReplyCtx: "ctx"}
	e.cmdContext(p, msg)

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "No active session") {
		t.Errorf("expected 'No active session', got: %q", sent[0])
	}
}

func TestCmdContext_EmptySession(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C901:U901"
	if _, err := store.Spawn(key, ""); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{SessionKey: key, ReplyCtx: "ctx"}
	e.cmdContext(p, msg)

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "(none)") {
		t.Errorf("expected '(none)' for empty pins, got: %q", sent[0])
	}
	if !strings.Contains(sent[0], "(not set)") {
		t.Errorf("expected '(not set)' for empty objective, got: %q", sent[0])
	}
}

func TestCmdContext_WithPins(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C902:U902"
	if _, err := store.Spawn(key, "build the feature"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "first pin", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin 1: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "second pin", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin 2: %v", err)
	}

	msg := &Message{SessionKey: key, ReplyCtx: "ctx"}
	e.cmdContext(p, msg)

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	reply := sent[0]
	if !strings.Contains(reply, "build the feature") {
		t.Errorf("expected objective in reply, got: %q", reply)
	}
	if !strings.Contains(reply, "1.") || !strings.Contains(reply, "first pin") {
		t.Errorf("expected pin 1 listed, got: %q", reply)
	}
	if !strings.Contains(reply, "2.") || !strings.Contains(reply, "second pin") {
		t.Errorf("expected pin 2 listed, got: %q", reply)
	}
}

// ── /forget tests ────────────────────────────────────────────────────────────

func TestCmdForget_ByIndex_Happy(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C910:U910"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "pin alpha", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin 1: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "pin beta", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin 2: %v", err)
	}

	msg := &Message{SessionKey: key, ReplyCtx: "ctx", UserID: "U910"}
	e.cmdForget(p, msg, []string{"1"})

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin after forget, got %d", len(sess.Pinned))
	}
	if sess.Pinned[0].Text != "pin beta" {
		t.Errorf("surviving pin = %q, want %q", sess.Pinned[0].Text, "pin beta")
	}
}

func TestCmdForget_ByIndex_OutOfRange(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C911:U911"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "only pin", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin: %v", err)
	}

	msg := &Message{SessionKey: key, ReplyCtx: "ctx", UserID: "U911"}
	e.cmdForget(p, msg, []string{"99"})

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "No pin #99") {
		t.Errorf("expected 'No pin #99', got: %q", sent[0])
	}
}

func TestCmdForget_ByText_Happy(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C912:U912"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "remember the deadline", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin 1: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "review the spec", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin 2: %v", err)
	}

	msg := &Message{SessionKey: key, ReplyCtx: "ctx", UserID: "U912"}
	e.cmdForget(p, msg, []string{"deadline"})

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin after text-match forget, got %d", len(sess.Pinned))
	}
	if sess.Pinned[0].Text != "review the spec" {
		t.Errorf("surviving pin = %q, want %q", sess.Pinned[0].Text, "review the spec")
	}
}

func TestCmdForget_ByText_Ambiguous(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C913:U913"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "review feature A", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin 1: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "review feature B", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin 2: %v", err)
	}

	msg := &Message{SessionKey: key, ReplyCtx: "ctx", UserID: "U913"}
	e.cmdForget(p, msg, []string{"review"})

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "Multiple matches") {
		t.Errorf("expected 'Multiple matches', got: %q", sent[0])
	}
}

func TestCmdForget_ByText_NoMatch(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C914:U914"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "some pinned text", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin: %v", err)
	}

	msg := &Message{SessionKey: key, ReplyCtx: "ctx", UserID: "U914"}
	e.cmdForget(p, msg, []string{"nonexistent"})

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "No pin matching") {
		t.Errorf("expected 'No pin matching', got: %q", sent[0])
	}
}

func TestCmdForget_NoArgs(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C915:U915"
	if _, err := store.Spawn(key, "objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	msg := &Message{SessionKey: key, ReplyCtx: "ctx", UserID: "U915"}
	e.cmdForget(p, msg, []string{})

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "Usage:") {
		t.Errorf("expected 'Usage:', got: %q", sent[0])
	}
}

// ── /reset-scope tests ───────────────────────────────────────────────────────

func TestCmdResetScope_Happy(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const key = "slack:C920:U920"
	if _, err := store.Spawn(key, "build the product"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if _, err := store.AddPin(key, sessv1.PinnedItem{Text: "pin one", PinnedAt: time.Now(), Source: "user_explicit"}); err != nil {
		t.Fatalf("AddPin: %v", err)
	}
	if _, err := store.IncrementTurn(key); err != nil {
		t.Fatalf("IncrementTurn: %v", err)
	}

	sessBeforeReset, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey before reset: %v", err)
	}
	turnCountBefore := sessBeforeReset.TurnCount

	msg := &Message{SessionKey: key, ReplyCtx: "ctx", UserID: "U920"}
	e.cmdResetScope(p, msg)

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "Scope reset") {
		t.Errorf("expected 'Scope reset', got: %q", sent[0])
	}

	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey after reset: %v", err)
	}
	if len(sess.Pinned) != 0 {
		t.Errorf("expected 0 pins after reset, got %d", len(sess.Pinned))
	}
	if sess.RootObjective != "" {
		t.Errorf("expected empty RootObjective after reset, got: %q", sess.RootObjective)
	}
	if sess.TurnCount != turnCountBefore {
		t.Errorf("TurnCount changed: before=%d after=%d (must be preserved)", turnCountBefore, sess.TurnCount)
	}
}

func TestCmdResetScope_NoSession(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	msg := &Message{SessionKey: "slack:C921:U921", ReplyCtx: "ctx"}
	e.cmdResetScope(p, msg)

	sent := p.getSent()
	if len(sent) == 0 {
		t.Fatal("expected a reply, got none")
	}
	if !strings.Contains(sent[0], "No active session") {
		t.Errorf("expected 'No active session', got: %q", sent[0])
	}
}
