package session

import (
	"errors"
	"time"
)

// MaxPinsPerSession is the maximum number of pinned items a session may hold.
// v1 cap; may revisit in v2 once usage data exists. Chosen to cover realistic
// "pin key facts" usage (~5-10 pins per session) with comfortable headroom.
const MaxPinsPerSession = 20

// MaxTurnHistory is the max TurnSnapshot entries kept per session.
const MaxTurnHistory = 20

// ActiveWindowSize is the number of recent turns included in the ACTIVE tier per turn assembly.
const ActiveWindowSize = 5

// MaxOptionalItems is the max OPTIONAL tier items included before budget truncation.
const MaxOptionalItems = 3

// ErrPinLimitReached is returned by AddPin when the session already holds
// MaxPinsPerSession pins.
var ErrPinLimitReached = errors.New("pin limit reached")

// ErrSessionNotFound is returned by store operations that require an existing
// session when no session exists for the given key.
var ErrSessionNotFound = errors.New("session not found")

// ErrPinNotFound is returned by RemovePin when idx is out of range.
var ErrPinNotFound = errors.New("pin not found")

// Tier classifies a working set item for assembly and eviction priority.
type Tier string

const (
	TierPinned      Tier = "pinned"      // compression-immune; never evicted
	TierActive      Tier = "active"      // rolling window of recent turns; evict oldest first
	TierOptional    Tier = "optional"    // examples, prior brainstorms; drop first under budget pressure
	TierQuarantined Tier = "quarantined" // rejected/outdated items; labeled, kept, down-weighted in prompt
	TierDropped     Tier = "dropped"     // tangents, off-scope; removed from working set entirely
)
// PinnedItem is a single item pinned by the user to survive eviction.
type PinnedItem struct {
	Text      string    `json:"text"`
	Source    string    `json:"source"`
	PinnedAt  time.Time `json:"pinned_at"`
	PinnedBy  string    `json:"pinned_by"`
	PinnedVia string    `json:"pinned_via,omitempty"` // "reply_to" when pinned by replying; empty for text-arg pins
	Tier      Tier      `json:"tier,omitempty"`       // empty → TierPinned
}

// TurnSnapshot captures one turn for the ACTIVE tier rolling window.
type TurnSnapshot struct {
	UserMessage UserMessage `json:"user_message"`
	TurnNum     int         `json:"turn_num"`
	Tier        Tier        `json:"tier"` // TierActive by default; TierQuarantined if flagged
}

// WorkingSet is rebuilt each turn before invoking Claude Code.
// Fields are populated by the Working Set Policy (working_set.go).
type WorkingSet struct {
	RootObjective     string         `json:"root_objective"`
	Pinned            []PinnedItem   `json:"pinned"`            // PINNED tier
	ActiveTurns       []TurnSnapshot `json:"active_turns"`      // ACTIVE tier
	OptionalItems     []PinnedItem   `json:"optional_items"`    // OPTIONAL tier
	QuarantinedItems  []PinnedItem   `json:"quarantined_items"` // QUARANTINED tier
	RecentUserMessage *UserMessage   `json:"recent_user_message"`
	// RecentToolResult is reserved for the most recent tool output per the spec's working_set rules.
	// v1 LIMITATION: this field is never populated — the engine does not currently track tool results.
	// v2 will wire tool result capture from the Claude Code invocation path (see docs/sessions.md
	// "Known v1 limitations"). Until v2, this field will always be nil in the marshalled JSON.
	RecentToolResult *ToolResult `json:"recent_tool_result"`
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
	SessionKey     string         `json:"session_key"`
	RootObjective  string         `json:"root_objective"`
	Pinned         []PinnedItem   `json:"pinned"`
	WorkingSet     WorkingSet     `json:"working_set"`
	TurnHistory    []TurnSnapshot `json:"turn_history"`
	TurnCount      int            `json:"turn_count"`
	// CorrectionCount is incremented each turn the user's message is detected as a correction.
	// Used by Meter v0 to compute correction_rate. Zero value is correct for pre-v1.4 sessions.
	CorrectionCount int       `json:"correction_count"`
	LastActivityTs time.Time  `json:"last_activity_ts"`
	CreatedAt      time.Time      `json:"created_at"`
}
