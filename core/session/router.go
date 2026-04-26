package session

import "time"

// sessionTTL is the idle timeout after which a session is considered expired.
// Conversations idle longer than this receive a fresh session on next message.
const sessionTTL = 30 * time.Minute

// RouteAction describes the decision the Router made for an incoming event.
type RouteAction int

const (
	// RouteActionSpawn means a new session was created for this event.
	RouteActionSpawn RouteAction = iota
	// RouteActionAttach means an existing session is still active; reuse it.
	RouteActionAttach
	// RouteActionIgnore means the event should not trigger a Claude Code turn.
	RouteActionIgnore
)

// SlackEvent carries the routing-relevant fields extracted from a Slack event
// before calling Router.Route. The Slack platform fills this from the raw
// slackevents.AppMentionEvent / MessageEvent / SlashCommand structs.
//
// ChannelType mapping:
//   - "im"      → direct message (no @mention needed)
//   - "channel" → public channel
//   - "group"   → private channel
//
// ThreadTS is empty when the message is top-level (no parent thread).
// For replies, ThreadTS is the ts of the thread root message.
type SlackEvent struct {
	ChannelID    string
	ChannelType  string
	UserID       string
	MessageTS    string
	ThreadTS     string
	IsBotMention bool
	ReceivedAt   time.Time
}

// RouteResult holds the Router's decision and the resolved session.
type RouteResult struct {
	Action     RouteAction
	SessionKey string
	// Session is non-nil when Action is Spawn or Attach.
	Session *Session
}

// Router maps incoming Slack events to sessions using the v1 routing rules.
// It is safe to call Route concurrently; thread-safety is delegated to the
// SessionStore (InMemorySessionStore uses a single coarse mutex).
type Router struct {
	store SessionStore
}

// NewRouter returns a Router backed by the provided store.
func NewRouter(store SessionStore) *Router {
	return &Router{store: store}
}

// Route applies the v1 routing rules to ev and returns a RouteResult.
// rootObjective is the text of the message that should seed a newly spawned
// session; it is ignored when an existing session is attached.
//
// Rules (applied in order):
//  1. DM (ChannelType == "im"): session_key = channel_id; spawn-or-replace.
//  2. Channel top-level @mention (IsBotMention, ThreadTS == ""): spawn new,
//     session_key = MessageTS (the new thread root).
//  3. Channel reply in thread (ThreadTS != ""): look up by ThreadTS; attach if
//     idle ≤ 30 min, otherwise spawn new (same session_key, new session_id).
//  4. Everything else: ignore.
func (r *Router) Route(ev SlackEvent, rootObjective string) (*RouteResult, error) {
	if ev.ChannelType == "im" {
		return r.spawnOrReplace(ev.ChannelID, rootObjective, ev.ReceivedAt)
	}

	threadIsReply := ev.ThreadTS != "" && ev.ThreadTS != ev.MessageTS

	if !threadIsReply {
		if ev.IsBotMention {
			// Rule 2: top-level @mention → always spawn a fresh session.
			// The spawning message's ts becomes the session_key so that
			// follow-up replies in the same thread route back here.
			sess, err := r.store.Spawn(ev.MessageTS, rootObjective)
			if err != nil {
				return nil, err
			}
			return &RouteResult{Action: RouteActionSpawn, SessionKey: ev.MessageTS, Session: sess}, nil
		}
		// Rule 4: top-level non-mention in a channel → ignore.
		return &RouteResult{Action: RouteActionIgnore}, nil
	}

	// Rule 3: reply in an existing thread.
	return r.spawnOrReplace(ev.ThreadTS, rootObjective, ev.ReceivedAt)
}

// spawnOrReplace looks up the session for sessionKey. If a session exists and
// is not idle beyond the TTL it attaches; otherwise it spawns a replacement.
func (r *Router) spawnOrReplace(sessionKey, rootObjective string, receivedAt time.Time) (*RouteResult, error) {
	sess, err := r.store.GetByKey(sessionKey)
	if err != nil {
		return nil, err
	}
	if sess != nil && receivedAt.Sub(sess.LastActivityTs) <= sessionTTL {
		return &RouteResult{Action: RouteActionAttach, SessionKey: sessionKey, Session: sess}, nil
	}
	newSess, err := r.store.Spawn(sessionKey, rootObjective)
	if err != nil {
		return nil, err
	}
	return &RouteResult{Action: RouteActionSpawn, SessionKey: sessionKey, Session: newSess}, nil
}
