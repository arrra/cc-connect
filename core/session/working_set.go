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

// BuildWorkingSet constructs a fresh WorkingSet for the current turn,
// routing each item into its tier bucket.
// recentMsg is the user message that just arrived.
// The session's existing WorkingSet.RecentToolResult carries forward from the
// prior turn — callers must call store.Update after the turn completes to
// persist any new tool result for the next BuildWorkingSet call.
func BuildWorkingSet(sess *Session, recentMsg *UserMessage) WorkingSet {
	var pinned, quarantined, optional []PinnedItem
	for _, item := range sess.Pinned {
		switch item.Tier {
		case TierQuarantined:
			quarantined = append(quarantined, item)
		case TierOptional:
			optional = append(optional, item)
		case TierDropped:
			// excluded from working set entirely
		default: // TierPinned or "" — compression-immune
			pinned = append(pinned, item)
		}
	}
	if len(optional) > MaxOptionalItems {
		optional = optional[:MaxOptionalItems]
	}

	// ACTIVE bucket: last ActiveWindowSize TierActive turns, most-recent-first.
	var activeTurns []TurnSnapshot
	for i := len(sess.TurnHistory) - 1; i >= 0 && len(activeTurns) < ActiveWindowSize; i-- {
		if sess.TurnHistory[i].Tier == TierActive {
			activeTurns = append(activeTurns, sess.TurnHistory[i])
		}
	}

	var toolResult *ToolResult
	if sess.WorkingSet.RecentToolResult != nil {
		tr := *sess.WorkingSet.RecentToolResult
		toolResult = &tr
	}

	if pinned == nil {
		pinned = []PinnedItem{}
	}

	return WorkingSet{
		RootObjective:     sess.RootObjective,
		Pinned:            pinned,
		ActiveTurns:       activeTurns,
		OptionalItems:     optional,
		QuarantinedItems:  quarantined,
		RecentUserMessage: recentMsg,
		RecentToolResult:  toolResult,
	}
}

// MarshalSystemContext serializes ws into the v1.3+ JSON format for injection
// into the Claude Code prompt. Shape: 7 fields covering all tier buckets.
func MarshalSystemContext(ws WorkingSet) (string, error) {
	type systemContext struct {
		RootObjective     string         `json:"root_objective"`
		Pinned            []PinnedItem   `json:"pinned"`
		ActiveTurns       []TurnSnapshot `json:"active_turns"`
		OptionalItems     []PinnedItem   `json:"optional_items"`
		QuarantinedItems  []PinnedItem   `json:"quarantined_items"`
		RecentUserMessage *UserMessage   `json:"recent_user_message"`
		RecentToolResult  *ToolResult    `json:"recent_tool_result"`
	}

	ctx := systemContext{
		RootObjective:     ws.RootObjective,
		Pinned:            ws.Pinned,
		ActiveTurns:       ws.ActiveTurns,
		OptionalItems:     ws.OptionalItems,
		QuarantinedItems:  ws.QuarantinedItems,
		RecentUserMessage: ws.RecentUserMessage,
		RecentToolResult:  ws.RecentToolResult,
	}
	if ctx.Pinned == nil {
		ctx.Pinned = []PinnedItem{}
	}
	if ctx.ActiveTurns == nil {
		ctx.ActiveTurns = []TurnSnapshot{}
	}
	if ctx.OptionalItems == nil {
		ctx.OptionalItems = []PinnedItem{}
	}
	if ctx.QuarantinedItems == nil {
		ctx.QuarantinedItems = []PinnedItem{}
	}

	b, err := json.Marshal(ctx)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
