package core

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	sessv1 "github.com/chenhg5/cc-connect/core/session"
	"github.com/chenhg5/cc-connect/core/hexmem"
)

// compactOutput2WS is a minimal memory_search.py --compact reply used by working-set
// assembly tests to exercise the combined hex_memory + session_context prompt path.
const compactOutput2WS = `    [1] source/alpha > topic1  (score: 1.00)
        recall result alpha

    [2] source/beta > topic2  (score: 0.85)
        recall result beta
`

// TestWorkingSetAssembly_InjectsCorrectSchema verifies that prependV1Context
// produces a prompt containing a <cc-connect:session_context> block whose JSON
// matches the 7-field v1.3+ schema and that active_turns is populated when the
// session has prior turn history (distinct from TestWorkingSet_InjectedSchemaShape
// which tests the zero-history baseline).
func TestWorkingSetAssembly_InjectsCorrectSchema(t *testing.T) {
	const sessionKey = "slack:C900:T900"
	const basePrompt = "what should I do next?"

	e, store, _ := newV1TestEngine(t, true)
	if _, err := store.Spawn(sessionKey, "assembly test objective"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Add a prior turn to TurnHistory so active_turns is non-empty.
	priorSnap := sessv1.TurnSnapshot{
		UserMessage: sessv1.UserMessage{Text: "prior user message", Ts: "1900000001.000001"},
		TurnNum:     1,
		Tier:        sessv1.TierActive,
	}
	if err := store.AppendTurn(sessionKey, priorSnap); err != nil {
		t.Fatalf("AppendTurn: %v", err)
	}

	injected := e.prependV1Context(sessionKey, basePrompt, "1900000002.000002", basePrompt)

	if !strings.Contains(injected, "<cc-connect:session_context>") {
		t.Fatalf("prompt missing <cc-connect:session_context> tag:\n%s", injected)
	}

	ctxJSON := extractContextJSON(t, injected)

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ctxJSON), &parsed); err != nil {
		t.Fatalf("unmarshal context JSON: %v\njson: %s", err, ctxJSON)
	}

	// 7-field v1.3+ schema — schema drift detection.
	wantFields := []string{
		"root_objective", "pinned", "active_turns",
		"optional_items", "quarantined_items",
		"recent_user_message", "recent_tool_result",
	}
	if len(parsed) != len(wantFields) {
		t.Errorf("expected %d top-level fields, got %d: %v", len(wantFields), len(parsed), fieldKeys(parsed))
	}
	for _, f := range wantFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("missing field %q in session context", f)
		}
	}

	// root_objective matches the spawn message.
	var rootObj string
	if err := json.Unmarshal(parsed["root_objective"], &rootObj); err != nil {
		t.Fatalf("unmarshal root_objective: %v", err)
	}
	if rootObj != "assembly test objective" {
		t.Errorf("root_objective = %q, want %q", rootObj, "assembly test objective")
	}

	// active_turns has the prior turn — non-empty baseline validates working set assembly.
	var activeTurns []sessv1.TurnSnapshot
	if err := json.Unmarshal(parsed["active_turns"], &activeTurns); err != nil {
		t.Fatalf("unmarshal active_turns: %v", err)
	}
	if len(activeTurns) != 1 {
		t.Errorf("active_turns: got %d items, want 1", len(activeTurns))
	} else if activeTurns[0].UserMessage.Text != "prior user message" {
		t.Errorf("active_turns[0].UserMessage.Text = %q, want %q", activeTurns[0].UserMessage.Text, "prior user message")
	}

	// base prompt still present at the end.
	if !strings.HasSuffix(strings.TrimSpace(injected), basePrompt) {
		t.Errorf("base prompt missing from injected result")
	}
}

// TestWorkingSetAssembly_HexRecallItemsAppearAsActive verifies the combined
// prompt produced when both applyAutoRecall (hex memory) and prependV1Context
// (session context) fire on the first turn:
//   - <cc-connect:hex_memory> block is prepended BEFORE <cc-connect:session_context>
//   - hex-recall items are present with [hex-recall] prefix
//   - <cc-connect:session_context> block still has the correct 7-field schema
//   - original prompt is preserved at the end
//
// This is the production path (engine.go:2124-2126). The two blocks are assembled
// independently; this test is the only one that verifies they coexist correctly.
func TestWorkingSetAssembly_HexRecallItemsAppearAsActive(t *testing.T) {
	const sessionKey = "slack:C901:T901"
	const basePrompt = "initial user message"

	var searchCalls atomic.Int32
	mockExec := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		searchCalls.Add(1)
		return []byte(compactOutput2WS), nil
	}

	e, store, _ := newV1TestEngine(t, true)
	if _, err := store.Spawn(sessionKey, "hex recall schema test"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// TurnCount==0 so applyAutoRecall fires.

	hexClient := hexmem.NewClientForTest("/fake/hex", "/fake/save.py", "/fake/search.py", mockExec)
	e.SetHexClient(hexClient)

	// Replicate production engine.go:2124-2126 ordering.
	promptContent := e.prependV1Context(sessionKey, basePrompt, "1900000010.000010", basePrompt)
	promptContent = e.applyAutoRecall(sessionKey, promptContent)

	// hex Search was called exactly once.
	if searchCalls.Load() != 1 {
		t.Errorf("hex Search called %d times, want 1", searchCalls.Load())
	}

	// <cc-connect:hex_memory> block is present and comes BEFORE session_context.
	hexIdx := strings.Index(promptContent, "<cc-connect:hex_memory>")
	sessIdx := strings.Index(promptContent, "<cc-connect:session_context>")
	if hexIdx < 0 {
		t.Fatalf("prompt missing <cc-connect:hex_memory> block:\n%s", promptContent)
	}
	if sessIdx < 0 {
		t.Fatalf("prompt missing <cc-connect:session_context> block:\n%s", promptContent)
	}
	if hexIdx >= sessIdx {
		t.Errorf("expected hex_memory block before session_context; hexIdx=%d sessIdx=%d", hexIdx, sessIdx)
	}

	// Hex-recall items appear with [hex-recall] prefix.
	if !strings.Contains(promptContent, "[hex-recall] recall result alpha") {
		t.Errorf("prompt missing [hex-recall] recall result alpha:\n%s", promptContent)
	}
	if !strings.Contains(promptContent, "[hex-recall] recall result beta") {
		t.Errorf("prompt missing [hex-recall] recall result beta:\n%s", promptContent)
	}

	// session_context JSON still has the 7-field schema (schema drift detection).
	ctxJSON := extractContextJSON(t, promptContent)
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(ctxJSON), &parsed); err != nil {
		t.Fatalf("unmarshal session context JSON: %v", err)
	}
	wantFields := []string{
		"root_objective", "pinned", "active_turns",
		"optional_items", "quarantined_items",
		"recent_user_message", "recent_tool_result",
	}
	if len(parsed) != len(wantFields) {
		t.Errorf("session_context: expected %d fields, got %d: %v", len(wantFields), len(parsed), fieldKeys(parsed))
	}
	for _, f := range wantFields {
		if _, ok := parsed[f]; !ok {
			t.Errorf("session_context: missing field %q", f)
		}
	}

	// root_objective is correct.
	var rootObj string
	if err := json.Unmarshal(parsed["root_objective"], &rootObj); err != nil {
		t.Fatalf("unmarshal root_objective: %v", err)
	}
	if rootObj != "hex recall schema test" {
		t.Errorf("root_objective = %q, want %q", rootObj, "hex recall schema test")
	}

	// Original prompt still at the end.
	if !strings.HasSuffix(strings.TrimSpace(promptContent), basePrompt) {
		t.Errorf("base prompt missing from final assembled prompt")
	}

}
