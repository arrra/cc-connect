package slack

import (
	"testing"

	"github.com/chenhg5/cc-connect/core"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
)

// TestSlashDispatch_Behavioral exercises the Slack platform's cc-prefix
// stripping and builtin-command guard in handleEvent (slash command path).
//
// The assertion is on msg.Content — the canonical command name the handler
// receives after stripping. This catches q-092-class regressions where the
// strip was present but the guard condition was inverted or wrong.
//
// Does NOT use mock-call assertions; asserts a real observable side effect
// (msg.Content reaching the handler).
func TestSlashDispatch_Behavioral(t *testing.T) {
	cases := []struct {
		name        string
		command     string // Slack slash command as delivered (e.g. "/ccrecall")
		text        string // Slack command text (after the command)
		wantContent string // expected msg.Content reaching handler
	}{
		{
			name:        "ccrecall strips to recall",
			command:     "/ccrecall",
			text:        "test query",
			wantContent: "/recall test query",
		},
		{
			name:        "ccforget strips to forget",
			command:     "/ccforget",
			text:        "1",
			wantContent: "/forget 1",
		},
		{
			name:        "ccreset-scope strips to reset-scope",
			command:     "/ccreset-scope",
			text:        "",
			wantContent: "/reset-scope",
		},
		{
			name:        "ccpromote strips to promote",
			command:     "/ccpromote",
			text:        "1",
			wantContent: "/promote 1",
		},
		{
			// /pin has no cc-prefix — passes through as-is (Slack allows /pin directly)
			name:        "pin dispatches as-is",
			command:     "/pin",
			text:        "foo",
			wantContent: "/pin foo",
		},
		{
			// cc-prefix guard: "md-custom" is not a builtin, so stripping "cc"
			// is suppressed and the full "/ccmd-custom" reaches the handler.
			// A broken guard (strip always) would produce "/md-custom" instead.
			name:        "ccmd-custom NOT stripped (guard: md-custom not a builtin)",
			command:     "/ccmd-custom",
			text:        "",
			wantContent: "/ccmd-custom",
		},
		{
			// Unknown command with no cc prefix — forwarded as-is.
			name:        "notreal dispatches as-is",
			command:     "/notreal",
			text:        "",
			wantContent: "/notreal",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotMsg *core.Message
			p := &Platform{
				allowFrom: "", // allow all users
				handler: func(_ core.Platform, msg *core.Message) {
					gotMsg = msg
				},
			}

			evt := socketmode.Event{
				Type: socketmode.EventTypeSlashCommand,
				Data: slack.SlashCommand{
					Command:   tc.command,
					Text:      tc.text,
					UserID:    "U123",
					UserName:  "testuser",
					ChannelID: "C456",
				},
				// Request: nil — skips socket.Ack() and thread-root fetch (no live socket)
			}
			p.handleEvent(evt)

			if gotMsg == nil {
				t.Fatalf("handler not called for command %q — dispatch never reached handler", tc.command)
			}
			if gotMsg.Content != tc.wantContent {
				t.Errorf("msg.Content = %q, want %q\n(cc-strip or guard is broken)", gotMsg.Content, tc.wantContent)
			}
		})
	}
}

// TestSlashDispatch_Behavioral_AllowList ensures that slash commands from
// unauthorized users are dropped before the handler is called.
func TestSlashDispatch_Behavioral_AllowList(t *testing.T) {
	called := false
	p := &Platform{
		allowFrom: "U-ALLOWED",
		handler: func(_ core.Platform, msg *core.Message) {
			called = true
		},
	}

	evt := socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: slack.SlashCommand{
			Command:   "/ccrecall",
			Text:      "test",
			UserID:    "U-OTHER", // not in allowFrom
			ChannelID: "C456",
		},
		Request: nil,
	}
	p.handleEvent(evt)

	if called {
		t.Error("handler called for unauthorized user — allowList not blocking slash commands")
	}
}
