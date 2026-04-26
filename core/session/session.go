package session

import "time"

// PinnedItem is a single item pinned by the user to survive eviction.
type PinnedItem struct {
	Text      string    `json:"text"`
	Source    string    `json:"source"`
	PinnedAt  time.Time `json:"pinned_at"`
	PinnedBy  string    `json:"pinned_by"`
}

// WorkingSet is rebuilt each turn before invoking Claude Code.
// Fields are populated by the Working Set Policy (working_set.go).
type WorkingSet struct {
	RootObjective    string         `json:"root_objective"`
	Pinned           []PinnedItem   `json:"pinned"`
	RecentUserMessage *UserMessage  `json:"recent_user_message"`
	RecentToolResult  *ToolResult   `json:"recent_tool_result"`
}

// UserMessage is a single user turn stored in the working set.
type UserMessage struct {
	Text string `json:"text"`
	Ts   string `json:"ts"`
}

// ToolResult is the most recent tool output from a prior turn.
type ToolResult struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Ts      string `json:"ts"`
}

// Session is one bounded conversation, scoped to a Slack thread or DM.
type Session struct {
	// SessionID is a UUID immutable for the life of this session instance.
	SessionID string `json:"session_id"`
	// SessionKey is the routing key (thread_ts for channels, channel_id for DMs).
	// The same key may be reused across sessions after TTL expiry.
	SessionKey     string      `json:"session_key"`
	RootObjective  string      `json:"root_objective"`
	Pinned         []PinnedItem `json:"pinned"`
	WorkingSet     WorkingSet  `json:"working_set"`
	TurnCount      int         `json:"turn_count"`
	LastActivityTs time.Time   `json:"last_activity_ts"`
	CreatedAt      time.Time   `json:"created_at"`
}
