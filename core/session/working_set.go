package session

import (
	"encoding/json"
	"strings"
)

// BuildWorkingSet constructs a fresh WorkingSet for the current turn.
// recentMsg is the user message that just arrived.
// The session's existing WorkingSet.RecentToolResult carries forward from the
// prior turn — callers must call store.Update after the turn completes to
// persist any new tool result for the next BuildWorkingSet call.
func BuildWorkingSet(sess *Session, recentMsg *UserMessage) WorkingSet {
	pinned := make([]PinnedItem, len(sess.Pinned))
	copy(pinned, sess.Pinned)

	var toolResult *ToolResult
	if sess.WorkingSet.RecentToolResult != nil {
		tr := *sess.WorkingSet.RecentToolResult
		toolResult = &tr
	}

	return WorkingSet{
		RootObjective:     sess.RootObjective,
		Pinned:            pinned,
		RecentUserMessage: recentMsg,
		RecentToolResult:  toolResult,
	}
}

// MarshalSystemContext serializes ws into the locked v1 JSON format for injection
// into the Claude Code prompt. The JSON shape is locked at 4 top-level fields;
// no additional fields may be added in v1.
func MarshalSystemContext(ws WorkingSet) (string, error) {
	type systemContext struct {
		RootObjective     string       `json:"root_objective"`
		Pinned            []PinnedItem `json:"pinned"`
		RecentUserMessage *UserMessage `json:"recent_user_message"`
		RecentToolResult  *ToolResult  `json:"recent_tool_result"`
	}

	ctx := systemContext{
		RootObjective:     ws.RootObjective,
		Pinned:            ws.Pinned,
		RecentUserMessage: ws.RecentUserMessage,
		RecentToolResult:  ws.RecentToolResult,
	}
	if ctx.Pinned == nil {
		ctx.Pinned = []PinnedItem{}
	}

	b, err := json.Marshal(ctx)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
