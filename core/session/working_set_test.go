package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestBuildWorkingSet_ThreeTurns verifies that BuildWorkingSet + MarshalSystemContext
// produce exactly the 4 locked fields and that only the current turn's user message
// appears (prior turn messages are evicted).
func TestBuildWorkingSet_ThreeTurns(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:C001:1700000000.000100"

	if _, err := store.Spawn(key, "help me refactor auth.go"); err != nil {
		t.Fatal(err)
	}

	msgs := []UserMessage{
		{Text: "turn 1 message", Ts: "1700000001.000001"},
		{Text: "turn 2 message", Ts: "1700000002.000002"},
		{Text: "turn 3 message", Ts: "1700000003.000003"},
	}

	var lastPrompt string

	for i, m := range msgs {
		// Reload session each turn to simulate per-turn store.GetByKey.
		current, err := store.GetByKey(key)
		if err != nil {
			t.Fatalf("turn %d: GetByKey: %v", i+1, err)
		}

		ws := BuildWorkingSet(current, &msgs[i])
		ctx, err := MarshalSystemContext(ws)
		if err != nil {
			t.Fatalf("turn %d: MarshalSystemContext: %v", i+1, err)
		}

		if i == 2 {
			lastPrompt = ctx
		}

		// Advance session state for next turn.
		current.TurnCount++
		current.LastActivityTs = time.Now()
		if err := store.Update(current); err != nil {
			t.Fatalf("turn %d: Update: %v", i+1, err)
		}
		_ = m
	}

	// Parse turn 3's serialized working set.
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lastPrompt), &parsed); err != nil {
		t.Fatalf("unmarshal turn 3 context: %v", err)
	}

	// Assert exactly 4 top-level fields.
	wantFields := []string{"root_objective", "pinned", "recent_user_message", "recent_tool_result"}
	if len(parsed) != len(wantFields) {
		t.Errorf("expected %d top-level fields, got %d: %v", len(wantFields), len(parsed), parsed)
	}
	for _, f := range wantFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("missing field %q in system context", f)
		}
	}

	// Assert root_objective is the spawning message.
	var rootObj string
	if err := json.Unmarshal(parsed["root_objective"], &rootObj); err != nil {
		t.Fatalf("unmarshal root_objective: %v", err)
	}
	if rootObj != "help me refactor auth.go" {
		t.Errorf("root_objective = %q, want %q", rootObj, "help me refactor auth.go")
	}

	// Assert recent_user_message is turn 3 (not turn 1 or 2).
	var recentMsg UserMessage
	if err := json.Unmarshal(parsed["recent_user_message"], &recentMsg); err != nil {
		t.Fatalf("unmarshal recent_user_message: %v", err)
	}
	if recentMsg.Text != "turn 3 message" {
		t.Errorf("recent_user_message.text = %q, want %q", recentMsg.Text, "turn 3 message")
	}
	if recentMsg.Ts != "1700000003.000003" {
		t.Errorf("recent_user_message.ts = %q, want %q", recentMsg.Ts, "1700000003.000003")
	}

	// Assert prior turn messages do NOT appear anywhere in the serialized context.
	for _, prior := range []string{"turn 1 message", "turn 2 message"} {
		if strings.Contains(lastPrompt, prior) {
			t.Errorf("prior turn message %q leaked into turn 3 context", prior)
		}
	}

	// Assert recent_tool_result is null (no tool results set in this test).
	if string(parsed["recent_tool_result"]) != "null" {
		t.Errorf("recent_tool_result = %s, want null (no tools used)", parsed["recent_tool_result"])
	}
}

// TestBuildWorkingSet_PinnedItemsCarryForward verifies that pinned items appear
// in the working set on every turn.
func TestBuildWorkingSet_PinnedItemsCarryForward(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:C002:1700000000.000200"

	sess, err := store.Spawn(key, "debug the CI pipeline")
	if err != nil {
		t.Fatal(err)
	}

	// Add a pin directly to the session.
	sess.Pinned = append(sess.Pinned, PinnedItem{
		Text:     "CI always fails on arm64",
		Source:   "user_explicit",
		PinnedAt: time.Now(),
		PinnedBy: "U001",
	})
	if err := store.Update(sess); err != nil {
		t.Fatal(err)
	}

	for turn := 1; turn <= 3; turn++ {
		current, _ := store.GetByKey(key)
		ws := BuildWorkingSet(current, &UserMessage{Text: "next step", Ts: "ts"})
		if len(ws.Pinned) != 1 {
			t.Errorf("turn %d: expected 1 pinned item, got %d", turn, len(ws.Pinned))
		}
		if ws.Pinned[0].Text != "CI always fails on arm64" {
			t.Errorf("turn %d: pinned item text = %q", turn, ws.Pinned[0].Text)
		}
	}
}

// TestBuildWorkingSet_ToolResultCarryForward verifies that a tool result stored
// in the session's WorkingSet.RecentToolResult carries forward into the next turn.
func TestBuildWorkingSet_ToolResultCarryForward(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:C003:1700000000.000300"

	sess, err := store.Spawn(key, "run tests")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate engine storing a tool result after turn 1.
	sess.WorkingSet.RecentToolResult = &ToolResult{
		Tool:    "Bash",
		Summary: "exit 0: 42 tests passed",
		Ts:      "1700000001.111",
	}
	if err := store.Update(sess); err != nil {
		t.Fatal(err)
	}

	// Turn 2 should carry the tool result forward.
	current, _ := store.GetByKey(key)
	ws := BuildWorkingSet(current, &UserMessage{Text: "fix the one failure", Ts: "1700000002.000"})
	if ws.RecentToolResult == nil {
		t.Fatal("expected RecentToolResult to carry forward from prior turn")
	}
	if ws.RecentToolResult.Tool != "Bash" {
		t.Errorf("RecentToolResult.Tool = %q, want %q", ws.RecentToolResult.Tool, "Bash")
	}
	if ws.RecentToolResult.Summary != "exit 0: 42 tests passed" {
		t.Errorf("RecentToolResult.Summary = %q", ws.RecentToolResult.Summary)
	}

	// Verify it appears in the marshaled context too.
	ctx, err := MarshalSystemContext(ws)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ctx, "42 tests passed") {
		t.Errorf("tool result summary not in marshaled context: %s", ctx)
	}
}

// TestBuildWorkingSet_TenTurnsNoLeak verifies that after 10 turns, the working
// set contains exactly the 4 locked fields and no prior-turn user messages leak.
// This is the spec's explicit 10-turn eviction requirement.
func TestBuildWorkingSet_TenTurnsNoLeak(t *testing.T) {
	store := NewInMemorySessionStore(nil, nil)
	key := "slack:C010:1700010000.000010"

	sess, err := store.Spawn(key, "ten-turn objective")
	if err != nil {
		t.Fatal(err)
	}

	// Pin one item so it must appear in every turn.
	sess.Pinned = append(sess.Pinned, PinnedItem{
		Text:     "always keep this",
		Source:   "user_explicit",
		PinnedAt: time.Now(),
		PinnedBy: "U010",
	})
	if err := store.Update(sess); err != nil {
		t.Fatal(err)
	}

	var lastCtx string

	for i := 1; i <= 10; i++ {
		current, err := store.GetByKey(key)
		if err != nil {
			t.Fatalf("turn %d: GetByKey: %v", i, err)
		}

		// Zero-pad so "turn-01" through "turn-09" cannot appear as substrings of "turn-10".
		msgText := fmt.Sprintf("user-msg-turn-%02d", i)
		msg := &UserMessage{Text: msgText, Ts: fmt.Sprintf("1700010000.%06d", i)}

		ws := BuildWorkingSet(current, msg)
		ctx, err := MarshalSystemContext(ws)
		if err != nil {
			t.Fatalf("turn %d: MarshalSystemContext: %v", i, err)
		}

		if i == 10 {
			lastCtx = ctx
		}

		current.TurnCount++
		current.LastActivityTs = time.Now()
		if err := store.Update(current); err != nil {
			t.Fatalf("turn %d: Update: %v", i, err)
		}
	}

	// Parse the turn-10 context.
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lastCtx), &parsed); err != nil {
		t.Fatalf("unmarshal turn 10 context: %v", err)
	}

	// Exactly 4 top-level fields.
	wantFields := []string{"root_objective", "pinned", "recent_user_message", "recent_tool_result"}
	if len(parsed) != len(wantFields) {
		t.Errorf("expected %d fields, got %d: %v", len(wantFields), len(parsed), parsed)
	}
	for _, f := range wantFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("missing field %q in turn 10 context", f)
		}
	}

	// Only turn-10's message must appear.
	var recentMsg UserMessage
	if err := json.Unmarshal(parsed["recent_user_message"], &recentMsg); err != nil {
		t.Fatalf("unmarshal recent_user_message: %v", err)
	}
	if recentMsg.Text != "user-msg-turn-10" {
		t.Errorf("recent_user_message.text = %q, want %q", recentMsg.Text, "user-msg-turn-10")
	}

	// Turns 1-9 must NOT appear anywhere in the serialized context.
	// Zero-padded format ensures none of these are substrings of "turn-10".
	for i := 1; i <= 9; i++ {
		prior := fmt.Sprintf("user-msg-turn-%02d", i)
		if strings.Contains(lastCtx, prior) {
			t.Errorf("prior turn message %q leaked into turn 10 context", prior)
		}
	}

	// Pinned item must still appear.
	if !strings.Contains(lastCtx, "always keep this") {
		t.Error("pinned item missing from turn 10 context")
	}
}

// TestMarshalSystemContext_FourFieldsOnly verifies the locked JSON shape.
func TestMarshalSystemContext_FourFieldsOnly(t *testing.T) {
	ws := WorkingSet{
		RootObjective:     "review PR #42",
		Pinned:            []PinnedItem{},
		RecentUserMessage: &UserMessage{Text: "lgtm?", Ts: "ts"},
		RecentToolResult:  nil,
	}
	ctx, err := MarshalSystemContext(ws)
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ctx), &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(parsed) != 4 {
		t.Errorf("expected exactly 4 fields, got %d: %v", len(parsed), parsed)
	}
}
