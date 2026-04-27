package core

import (
	"strings"
	"testing"
)

// TestSlashDispatch_Behavioral_Engine exercises engine.handleCommand dispatch
// for each v1.x builtin command and the unknown-command fallback path.
//
// Tests assert on reply content (a real side effect captured by
// stubPlatformEngine.sent) — not on mock call counts. This catches placebos
// where a case exists in the dispatch switch but the handler body never runs
// or sends the wrong reply.
//
// v1Store and hexClient are intentionally nil (engine constructed with
// newTestEngine) so each command exercises its "feature disabled" guard,
// producing a deterministic, unique reply per command.
func TestSlashDispatch_Behavioral_Engine(t *testing.T) {
	cases := []struct {
		name      string
		raw       string // raw slash command string as the engine receives it
		wantReply string // expected substring in the first reply
	}{
		{
			// /pin without v1Store — "Sessions feature disabled" guard fires.
			// Would have caught q-087 if /pin was wired but v1Store wasn't set.
			name:      "pin without v1Store",
			raw:       "/pin foo",
			wantReply: "Sessions feature disabled",
		},
		{
			// /forget without v1Store — same guard, different handler.
			name:      "forget without v1Store",
			raw:       "/forget 1",
			wantReply: "Sessions feature disabled",
		},
		{
			// /reset-scope without v1Store.
			name:      "reset-scope without v1Store",
			raw:       "/reset-scope",
			wantReply: "Sessions feature disabled",
		},
		{
			// /promote without v1Store.
			name:      "promote without v1Store",
			raw:       "/promote 1",
			wantReply: "Sessions feature disabled",
		},
		{
			// /recall without hexClient — "Hex memory is not enabled" guard fires.
			// Would have caught q-087 if /recall was in the switch but hexClient wasn't set.
			name:      "recall without hexClient",
			raw:       "/recall test query",
			wantReply: "Hex memory is not enabled",
		},
		{
			// Unknown command — engine sends MsgUnknownCommand and returns false.
			// Asserts the "forwarding to agent" notice fires (not a silent no-op).
			name:      "notreal command produces unknown-command notice",
			raw:       "/notreal",
			wantReply: "is not a cc-connect command",
		},
		{
			// cc-prefixed non-builtin reaches the engine unstripped (Slack layer
			// guarded the strip). Confirms the engine's default path handles it
			// the same as any other unknown command.
			name:      "ccmd-custom reaches engine as unknown command",
			raw:       "/ccmd-custom",
			wantReply: "is not a cc-connect command",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newTestEngine() // v1Store=nil, hexClient=nil
			p := &stubPlatformEngine{n: "test"}
			msg := &Message{
				SessionKey: "slack:C123",
				UserID:     "U123",
				Platform:   "slack",
				ReplyCtx:   "ctx",
			}

			e.handleCommand(p, msg, tc.raw)

			sent := p.getSent()
			if len(sent) == 0 {
				t.Fatalf("no reply sent for %q — handler dispatched but produced no output", tc.raw)
			}
			if !strings.Contains(sent[0], tc.wantReply) {
				t.Errorf("reply = %q\nwant substring: %q", sent[0], tc.wantReply)
			}
		})
	}
}
