package slack

import (
	"testing"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// TestSlackMessageShortcutHandler_RoutesToEngine verifies that the
// EventTypeInteractive handler routes pin_message shortcuts to the registered
// shortcutHandler function with the correct arguments.
func TestSlackMessageShortcutHandler_RoutesToEngine(t *testing.T) {
	type call struct {
		sessionKey  string
		messageText string
		userID      string
		threadTS    string
	}

	var calls []call
	handler := func(sessionKey, messageText, userID, threadTS string) error {
		calls = append(calls, call{sessionKey, messageText, userID, threadTS})
		return nil
	}

	p := &Platform{
		allowFrom:       "",  // allow all
		shortcutHandler: handler,
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type:       slack.InteractionTypeMessageAction,
			CallbackID: callbackIDPinMessage,
			Channel:    slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "C1"}}},
			User:       slack.User{ID: "U001"},
			Message: slack.Message{
				Msg: slack.Msg{
					Text:            "hello",
					ThreadTimestamp: "t1",
				},
			},
		},
		Request: nil, // nil request skips socket.Ack (no live socket in tests)
	}

	p.handleEvent(evt)

	if len(calls) != 1 {
		t.Fatalf("expected 1 shortcutHandler call, got %d", len(calls))
	}
	got := calls[0]
	if got.sessionKey != "slack:C1:t1" {
		t.Errorf("sessionKey = %q, want %q", got.sessionKey, "slack:C1:t1")
	}
	if got.messageText != "hello" {
		t.Errorf("messageText = %q, want %q", got.messageText, "hello")
	}
	if got.userID != "U001" {
		t.Errorf("userID = %q, want %q", got.userID, "U001")
	}
	if got.threadTS != "t1" {
		t.Errorf("threadTS = %q, want %q", got.threadTS, "t1")
	}
}

// TestSlackMessageShortcutHandler_SharedSession verifies per-channel session key
// when shareSessionInChannel is true.
func TestSlackMessageShortcutHandler_SharedSession(t *testing.T) {
	var gotKey string
	p := &Platform{
		allowFrom:             "",
		shareSessionInChannel: true,
		shortcutHandler: func(sessionKey, _, _, _ string) error {
			gotKey = sessionKey
			return nil
		},
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type:       slack.InteractionTypeMessageAction,
			CallbackID: callbackIDPinMessage,
			Channel:    slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "C99"}}},
			User:       slack.User{ID: "U002"},
			Message:    slack.Message{Msg: slack.Msg{Text: "shared"}},
		},
		Request: nil,
	}

	p.handleEvent(evt)

	if gotKey != "slack:C99" {
		t.Errorf("sessionKey = %q, want %q", gotKey, "slack:C99")
	}
}

// TestSlackMessageShortcutHandler_WrongCallbackIDIgnored verifies that an
// interactive event with a non-pin_message callback_id does not call the handler.
func TestSlackMessageShortcutHandler_WrongCallbackIDIgnored(t *testing.T) {
	called := false
	p := &Platform{
		allowFrom: "",
		shortcutHandler: func(_, _, _, _ string) error {
			called = true
			return nil
		},
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type:       slack.InteractionTypeMessageAction,
			CallbackID: "pin_random",
			Channel:    slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "C1"}}},
			User:       slack.User{ID: "U001"},
			Message:    slack.Message{Msg: slack.Msg{Text: "ignored"}},
		},
		Request: nil,
	}

	p.handleEvent(evt)

	if called {
		t.Error("shortcutHandler should not be called for unknown callback_id")
	}
}

// TestSlackMessageShortcutHandler_UnauthorizedUserIgnored verifies that shortcuts
// from users not in the allowlist are silently dropped.
func TestSlackMessageShortcutHandler_UnauthorizedUserIgnored(t *testing.T) {
	called := false
	p := &Platform{
		allowFrom: "U-ALLOWED",
		shortcutHandler: func(_, _, _, _ string) error {
			called = true
			return nil
		},
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type:       slack.InteractionTypeMessageAction,
			CallbackID: callbackIDPinMessage,
			Channel:    slack.Channel{GroupConversation: slack.GroupConversation{Conversation: slack.Conversation{ID: "C1"}}},
			User:       slack.User{ID: "U-OTHER"},
			Message:    slack.Message{Msg: slack.Msg{Text: "unauthorized"}},
		},
		Request: nil,
	}

	p.handleEvent(evt)

	if called {
		t.Error("shortcutHandler should not be called for unauthorized user")
	}
}
