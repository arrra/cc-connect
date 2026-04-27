package session

import (
	"encoding/json"
	"strings"
)

// ChannelKeyFromSessionKey parses a session key and returns the channel-level key
// and whether the key is thread-scoped. Thread keys have the form "platform:channel:thread_ts";
// channel keys have the form "platform:channel".
//
// Examples:
//
//	"slack:C123:1234.5678" → ("slack:C123", true)
//	"slack:C123"           → ("", false)
func ChannelKeyFromSessionKey(sessionKey string) (channelKey string, hasThread bool) {
	parts := strings.SplitN(sessionKey, ":", 3)
	if len(parts) == 3 && parts[2] != "" {
		return parts[0] + ":" + parts[1], true
	}
	return "", false
}

// MergeInheritedPins merges channel-level pins with thread-local pins.
// Channel pins appear first; thread pins follow. When both contain a pin with the
// same Text, the thread pin replaces the channel pin (thread wins on conflict).
func MergeInheritedPins(channelPins, threadPins []PinnedItem) []PinnedItem {
	threadByText := make(map[string]PinnedItem, len(threadPins))
	for _, p := range threadPins {
		threadByText[p.Text] = p
	}

	result := make([]PinnedItem, 0, len(channelPins)+len(threadPins))
	channelTexts := make(map[string]struct{}, len(channelPins))
	for _, p := range channelPins {
		channelTexts[p.Text] = struct{}{}
		if tp, ok := threadByText[p.Text]; ok {
			result = append(result, tp)
		} else {
			result = append(result, p)
		}
	}
	for _, p := range threadPins {
		if _, ok := channelTexts[p.Text]; !ok {
			result = append(result, p)
		}
	}
	return result
}

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
