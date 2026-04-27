package core

// Shared fixtures for v1 sessions integration tests (t-1 through t-9).
//
// Architecture note: cmdPin does not call conversations.replies directly —
// the Slack platform layer (platform/slack/slack.go fetchThreadRootText) fetches
// the parent message text and sets msg.ParentText before calling the engine.
// Engine-level tests therefore inject ParentText directly rather than mocking
// a Slack API client. Testing the platform → engine binding (including
// conversations.replies error paths) requires a live Slack smoke test.

import (
	"context"
	"log/slog"
	"sync"
	"testing"

	sessv1 "github.com/chenhg5/cc-connect/core/session"
)

// newV1TestEngine creates a minimal Engine for v1 sessions integration tests.
// If flagOn is true, a fresh in-memory session store is attached (v1 enabled).
// Returns the engine, the session store (nil when flagOn=false), and the platform stub.
func newV1TestEngine(t testing.TB, flagOn bool) (*Engine, sessv1.SessionStore, *stubPlatformEngine) {
	t.Helper()
	p := &stubPlatformEngine{n: "v1-test"}
	e := NewEngine("v1-test", &stubAgent{}, []Platform{p}, "", LangEnglish)
	var store sessv1.SessionStore
	if flagOn {
		store = sessv1.NewInMemorySessionStore(nil, nil)
		e.SetV1Store(store)
	}
	return e, store, p
}

// capturedSlogHandler implements slog.Handler and captures every record emitted.
// Safe for concurrent use.
type capturedSlogHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturedSlogHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *capturedSlogHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *capturedSlogHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *capturedSlogHandler) WithGroup(_ string) slog.Handler      { return h }

// hasMsg reports whether any captured record has the given message string.
func (h *capturedSlogHandler) hasMsg(msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == msg {
			return true
		}
	}
	return false
}

// attrKeys returns the slog attribute keys for the first record matching msg.
// Returns nil if no such record exists.
func (h *capturedSlogHandler) attrKeys(msg string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.records {
		if r.Message == msg {
			var keys []string
			r.Attrs(func(a slog.Attr) bool {
				keys = append(keys, a.Key)
				return true
			})
			return keys
		}
	}
	return nil
}

// installSlogCapture replaces the default slog handler with a capturedSlogHandler
// and restores the original on test cleanup.
func installSlogCapture(t testing.TB) *capturedSlogHandler {
	t.Helper()
	h := &capturedSlogHandler{}
	old := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(old) })
	return h
}

// synthesizeMsg builds a *Message for engine-level integration tests.
// Set parentText to non-empty to simulate a reply-to /pin context
// (the Slack platform layer pre-fetches parent text and sets this field).
func synthesizeMsg(sessionKey, content, userID, replyCtx, parentText string) *Message {
	return &Message{
		SessionKey: sessionKey,
		Content:    content,
		UserID:     userID,
		ReplyCtx:   replyCtx,
		ParentText: parentText,
	}
}
