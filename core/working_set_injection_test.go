package core

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	sessv1 "github.com/chenhg5/cc-connect/core/session"
)

// TestWorkingSet_InjectedSchemaShape (t-8) verifies the full injection path:
//   - prependV1Context injects a JSON block with exactly 4 locked fields
//   - root_objective is present and correct
//   - recent_user_message is populated with the current turn's message
//   - recent_tool_result is null (documented v1 limitation)
//   - pinned[] contains all pinned items (carry-forward across turns)
//   - Schema has no extra fields (regression guard against silent prompt expansion)
//
// "Running a turn" at the engine level means calling prependV1Context, which is the
// seam between the engine and the v1 session context. processInteractiveEvents cannot
// be tested in isolation because it blocks on agent events with the stub agent.
func TestWorkingSet_InjectedSchemaShape(t *testing.T) {
	p := &stubPlatformEngine{n: "test"}
	e := NewEngine("test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)

	const (
		key    = "slack:C800:U800"
		userID = "U800"
	)
	if _, err := store.Spawn(key, "test root objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Pin item A.
	msg := &Message{SessionKey: key, ReplyCtx: "ctx", UserID: userID}
	e.cmdPin(p, msg, []string{"item", "A"})

	// Pin item B.
	p.clearSent()
	e.cmdPin(p, msg, []string{"item", "B"})

	// --- Turn 1: marshal via prependV1Context and parse the injected JSON block ---
	const turn1Msg = "first user message"
	injected1 := e.prependV1Context(key, turn1Msg, "1800000001.000001", turn1Msg)
	if injected1 == turn1Msg {
		t.Fatal("prependV1Context returned original prompt unchanged — expected injected context block")
	}
	if !strings.Contains(injected1, "<cc-connect:session_context>") {
		t.Fatalf("injected context missing wrapper tag, got:\n%s", injected1)
	}

	// Extract the JSON block between the wrapper tags.
	ctx1JSON := extractContextJSON(t, injected1)

	var ctx1 map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ctx1JSON), &ctx1); err != nil {
		t.Fatalf("unmarshal context JSON: %v\njson: %s", err, ctx1JSON)
	}

	// 5. Schema has no extra fields — exactly 4.
	wantFields := []string{"root_objective", "pinned", "recent_user_message", "recent_tool_result"}
	if len(ctx1) != len(wantFields) {
		t.Errorf("turn 1: expected %d top-level fields, got %d: %v", len(wantFields), len(ctx1), fieldKeys(ctx1))
	}
	for _, f := range wantFields {
		if _, ok := ctx1[f]; !ok {
			t.Errorf("turn 1: missing field %q", f)
		}
	}

	// 1. root_objective present.
	var rootObj string
	if err := json.Unmarshal(ctx1["root_objective"], &rootObj); err != nil {
		t.Fatalf("unmarshal root_objective: %v", err)
	}
	if rootObj != "test root objective" {
		t.Errorf("root_objective = %q, want %q", rootObj, "test root objective")
	}

	// 2. recent_user_message populated with the first message.
	var recentMsg1 sessv1.UserMessage
	if err := json.Unmarshal(ctx1["recent_user_message"], &recentMsg1); err != nil {
		t.Fatalf("unmarshal recent_user_message: %v", err)
	}
	if recentMsg1.Text != turn1Msg {
		t.Errorf("turn 1: recent_user_message.text = %q, want %q", recentMsg1.Text, turn1Msg)
	}

	// 3. recent_tool_result is null (v1 limitation — engine never populates this).
	if string(ctx1["recent_tool_result"]) != "null" {
		t.Errorf("turn 1: recent_tool_result = %s, want null (v1 limitation: engine does not capture tool results)", ctx1["recent_tool_result"])
	}

	// 4. pinned[] contains both A and B.
	var pinned1 []sessv1.PinnedItem
	if err := json.Unmarshal(ctx1["pinned"], &pinned1); err != nil {
		t.Fatalf("unmarshal pinned: %v", err)
	}
	if len(pinned1) != 2 {
		t.Fatalf("turn 1: pinned[] has %d items, want 2", len(pinned1))
	}
	pinTexts1 := map[string]bool{pinned1[0].Text: true, pinned1[1].Text: true}
	if !pinTexts1["item A"] {
		t.Errorf("turn 1: pinned[] missing %q, got texts: %v", "item A", pinTexts1)
	}
	if !pinTexts1["item B"] {
		t.Errorf("turn 1: pinned[] missing %q, got texts: %v", "item B", pinTexts1)
	}

	// --- Advance session state (simulate turn completion) ---
	sess, err := store.GetByKey(key)
	if err != nil {
		t.Fatalf("GetByKey for turn advance: %v", err)
	}
	sess.TurnCount++
	sess.LastActivityTs = time.Now()
	if err := store.Update(sess); err != nil {
		t.Fatalf("Update for turn advance: %v", err)
	}

	// --- Turn 2: assert recent_user_message updated, pins still carry forward ---
	const turn2Msg = "second user message"
	injected2 := e.prependV1Context(key, turn2Msg, "1800000002.000002", turn2Msg)
	ctx2JSON := extractContextJSON(t, injected2)

	var ctx2 map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ctx2JSON), &ctx2); err != nil {
		t.Fatalf("unmarshal turn 2 context JSON: %v", err)
	}

	// recent_user_message updated to the second message.
	var recentMsg2 sessv1.UserMessage
	if err := json.Unmarshal(ctx2["recent_user_message"], &recentMsg2); err != nil {
		t.Fatalf("turn 2: unmarshal recent_user_message: %v", err)
	}
	if recentMsg2.Text != turn2Msg {
		t.Errorf("turn 2: recent_user_message.text = %q, want %q", recentMsg2.Text, turn2Msg)
	}

	// pinned[] still contains A and B (carry-forward proof).
	var pinned2 []sessv1.PinnedItem
	if err := json.Unmarshal(ctx2["pinned"], &pinned2); err != nil {
		t.Fatalf("turn 2: unmarshal pinned: %v", err)
	}
	if len(pinned2) != 2 {
		t.Fatalf("turn 2: pinned[] has %d items, want 2 (carry-forward failed)", len(pinned2))
	}
	pinTexts2 := map[string]bool{pinned2[0].Text: true, pinned2[1].Text: true}
	if !pinTexts2["item A"] {
		t.Errorf("turn 2: pinned[] missing %q after carry-forward", "item A")
	}
	if !pinTexts2["item B"] {
		t.Errorf("turn 2: pinned[] missing %q after carry-forward", "item B")
	}

	// recent_tool_result STILL null (v1 limitation persists).
	if string(ctx2["recent_tool_result"]) != "null" {
		t.Errorf("turn 2: recent_tool_result = %s, want null (still the v1 limitation, not silently fixed)", ctx2["recent_tool_result"])
	}

	// Schema still exactly 4 fields on turn 2 (no silent schema expansion).
	if len(ctx2) != len(wantFields) {
		t.Errorf("turn 2: expected %d fields, got %d: %v", len(wantFields), len(ctx2), fieldKeys(ctx2))
	}
}

// extractContextJSON pulls the JSON payload out of a prependV1Context result.
// The format is: "<cc-connect:session_context>\n{JSON}\n</cc-connect:session_context>\n\n{original prompt}"
func extractContextJSON(t testing.TB, s string) string {
	t.Helper()
	start := strings.Index(s, "<cc-connect:session_context>")
	end := strings.Index(s, "</cc-connect:session_context>")
	if start < 0 || end < 0 {
		t.Fatalf("session_context tags not found in injected string:\n%s", s)
	}
	inner := s[start+len("<cc-connect:session_context>") : end]
	return strings.TrimSpace(inner)
}

// fieldKeys returns the keys of a map[string]json.RawMessage for error messages.
func fieldKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
