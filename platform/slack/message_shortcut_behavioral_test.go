package slack

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/chenhg5/cc-connect/core"
	sessv1 "github.com/chenhg5/cc-connect/core/session"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// noopAgent satisfies core.Agent without doing anything. HandleMessageShortcut
// never calls the agent; this stub exists only to satisfy NewEngine's signature.
type noopAgent struct{}

func (noopAgent) Name() string                                                         { return "noop" }
func (noopAgent) StartSession(_ context.Context, _ string) (core.AgentSession, error) { return nil, nil }
func (noopAgent) ListSessions(_ context.Context) ([]core.AgentSessionInfo, error)     { return nil, nil }
func (noopAgent) Stop() error                                                          { return nil }

// behavioralSlogHandler captures slog records for assertion.
type behavioralSlogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *behavioralSlogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *behavioralSlogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *behavioralSlogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *behavioralSlogHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *behavioralSlogHandler) hasMsg(msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == msg {
			return true
		}
	}
	return false
}

func installBehavioralSlog(t testing.TB) *behavioralSlogHandler {
	t.Helper()
	h := &behavioralSlogHandler{}
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	return h
}

// newShortcutBehavioralSetup creates a Platform wired to a real Engine with a
// v1 session store. Calling handleEvent with a pin_message shortcut fires the
// full production path: platform dispatch → engine.HandleMessageShortcut → store.
func newShortcutBehavioralSetup(t testing.TB) (*Platform, sessv1.SessionStore) {
	t.Helper()
	p := &Platform{allowFrom: ""}
	e := core.NewEngine("test", noopAgent{}, []core.Platform{p}, "", core.LangEnglish)
	store := sessv1.NewInMemorySessionStore(nil, nil)
	e.SetV1Store(store)
	p.shortcutHandler = e.HandleMessageShortcut
	return p, store
}

func pinMessageEvent(channelID, userID, msgText, threadTS string) socketmode.Event {
	return socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type:       slack.InteractionTypeMessageAction,
			CallbackID: callbackIDPinMessage,
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{ID: channelID},
				},
			},
			User: slack.User{ID: userID},
			Message: slack.Message{
				Msg: slack.Msg{
					Text:            msgText,
					ThreadTimestamp: threadTS,
				},
			},
		},
		Request: nil, // nil skips socket.Ack (no live Slack socket in tests)
	}
}

// TestMessageShortcut_Behavioral_PinsParentMessage exercises the full Slack
// platform → engine dispatch path for a pin_message shortcut and asserts the
// pin lands in the v1 session store with the correct fields.
//
// This test would have caught q-070: SetMessageShortcutHandler declared but
// never called from main.go, leaving shortcutHandler nil so the engine's pin
// logic was never reachable.
func TestMessageShortcut_Behavioral_PinsParentMessage(t *testing.T) {
	logs := installBehavioralSlog(t)
	p, store := newShortcutBehavioralSetup(t)

	const (
		channelID  = "C123"
		userID     = "U001"
		msgText    = "this is the message I want pinned"
		threadTS   = "1234.5678"
		sessionKey = "slack:C123:1234.5678"
	)

	evt := pinMessageEvent(channelID, userID, msgText, threadTS)
	p.handleEvent(evt)

	// Assert 1: pin landed in the store.
	sess, err := store.GetByKey(sessionKey)
	if err != nil {
		t.Fatalf("store.GetByKey(%q): %v", sessionKey, err)
	}
	if sess == nil {
		t.Fatalf("no v1 session for %q — HandleMessageShortcut not called or SpawnOrAttach failed", sessionKey)
	}
	if len(sess.Pinned) != 1 {
		t.Fatalf("expected 1 pin in store, got %d", len(sess.Pinned))
	}
	pin := sess.Pinned[0]
	if pin.Text != msgText {
		t.Errorf("pin.Text = %q, want %q", pin.Text, msgText)
	}
	if pin.PinnedVia != "message_shortcut" {
		t.Errorf("pin.PinnedVia = %q, want %q", pin.PinnedVia, "message_shortcut")
	}
	if pin.PinnedBy != userID {
		t.Errorf("pin.PinnedBy = %q, want %q", pin.PinnedBy, userID)
	}

	// Assert 2: slog emitted the dispatch record (platform-side debug log).
	// Ack assertion deferred: the ack path requires a live socketmode.Client;
	// mocking it adds significant surface with low marginal value here.
	if !logs.hasMsg("slack: message shortcut") {
		t.Error("slog: expected 'slack: message shortcut' debug record — dispatch path not reached")
	}
}

// TestMessageShortcut_NoHandlerWired_GracefulNoOp simulates the q-070 bug: the
// engine's SetMessageShortcutHandler is never called (shortcutHandler == nil).
// The platform must not panic and must emit a warning; the store must be untouched.
func TestMessageShortcut_NoHandlerWired_GracefulNoOp(t *testing.T) {
	logs := installBehavioralSlog(t)

	// Platform with NO shortcutHandler wired — simulates q-070 production state.
	p := &Platform{allowFrom: ""}

	evt := pinMessageEvent("C123", "U001", "some message", "1234.5678")
	p.handleEvent(evt) // must not panic

	const wantMsg = "slack: message shortcut received but no handler registered"
	if !logs.hasMsg(wantMsg) {
		t.Errorf("slog: expected warning %q, not found — nil-handler path not guarded", wantMsg)
	}
}
