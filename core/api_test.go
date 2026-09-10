package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleSend_AllowsAttachmentOnly(t *testing.T) {
	engine := NewEngine("test", &stubAgent{}, []Platform{&stubMediaPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}, "", LangEnglish)
	engine.interactiveStates["session-1"] = &interactiveState{
		platform: &stubMediaPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}},
		replyCtx: "reply-ctx",
	}

	api := &APIServer{engines: map[string]*Engine{"test": engine}}
	reqBody := SendRequest{
		Project:    "test",
		SessionKey: "session-1",
		Images: []ImageAttachment{{
			MimeType: "image/png",
			Data:     []byte("img"),
			FileName: "chart.png",
		}},
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/send", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handleSend(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

// TestHandlePrompt_RejectsMissingSessionKey pins that POST /prompt requires
// session_key: an automation caller (hex-events) never has an ambiguous
// target, so a missing key is a caller bug, not something to guess at.
func TestHandlePrompt_RejectsMissingSessionKey(t *testing.T) {
	engine := NewEngine("test", &stubAgent{}, []Platform{&stubMediaPlatform{stubPlatformEngine: stubPlatformEngine{n: "test"}}}, "", LangEnglish)
	api := &APIServer{engines: map[string]*Engine{"test": engine}}

	reqBody := PromptRequest{Project: "test", Message: "escalation: task T1 blocked"}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/prompt", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handlePrompt(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body=%s, want %d", rec.Code, rec.Body.String(), http.StatusBadRequest)
	}
}

// TestHandlePrompt_UnknownProject pins handlePrompt's project resolution:
// with two engines registered, a project name matching neither is a 404 (the
// single-engine default fallback must NOT apply when there is more than one
// engine — same rule as handleSend).
func TestHandlePrompt_UnknownProject(t *testing.T) {
	engineA := NewEngine("alpha", &stubAgent{}, []Platform{&stubMediaPlatform{stubPlatformEngine: stubPlatformEngine{n: "alpha"}}}, "", LangEnglish)
	engineB := NewEngine("beta", &stubAgent{}, []Platform{&stubMediaPlatform{stubPlatformEngine: stubPlatformEngine{n: "beta"}}}, "", LangEnglish)
	api := &APIServer{engines: map[string]*Engine{"alpha": engineA, "beta": engineB}}

	reqBody := PromptRequest{Project: "gamma", SessionKey: "alpha:channel-1", Message: "hello"}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/prompt", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handlePrompt(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body=%s, want %d", rec.Code, rec.Body.String(), http.StatusNotFound)
	}
}

// TestHandlePrompt_QueuesPromptForSession pins the full happy path: POST
// /prompt injects the message into the named project's session as an agent
// prompt via the same handleMessage entry the platforms use (NOT
// SendToSessionWithAttachments — that only posts outbound text), the
// platform's reply context gets reconstructed for that session key, and the
// agent's reply lands back on the platform.
func TestHandlePrompt_QueuesPromptForSession(t *testing.T) {
	platform := &stubCronReplyTargetPlatform{
		stubPlatformEngine: stubPlatformEngine{n: "discord"},
	}
	agentSession := newResultAgentSession("escalation ack")
	agent := &resultAgent{session: agentSession}
	engine := NewEngine("test", agent, []Platform{platform}, "", LangEnglish)
	defer engine.cancel()

	api := &APIServer{engines: map[string]*Engine{"test": engine}}

	reqBody := PromptRequest{
		Project:    "test",
		SessionKey: "discord:channel-1:user-1",
		Message:    "escalation: task T1 blocked",
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/prompt", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	api.handlePrompt(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s, want %d", rec.Code, rec.Body.String(), http.StatusOK)
	}

	if platform.reconstructSessionKey != "discord:channel-1:user-1" {
		t.Fatalf("ReconstructReplyCtx sessionKey = %q, want %q", platform.reconstructSessionKey, "discord:channel-1:user-1")
	}

	// handleMessage dispatches the actual turn on a goroutine
	// (`go e.processInteractiveMessageWith(...)`); poll with a bounded
	// deadline for its effects rather than assume a fixed sleep is enough.
	pollUntil(t, 2*time.Second, func() bool {
		return len(agentSession.SentPrompts()) > 0 && len(platform.getSent()) > 0
	})

	prompts := agentSession.SentPrompts()
	if len(prompts) != 1 || !strings.Contains(prompts[0], "escalation: task T1 blocked") {
		t.Fatalf("agent prompts = %#v, want prompt containing the escalation message", prompts)
	}

	sent := platform.getSent()
	if len(sent) != 1 || sent[0] != "escalation ack" {
		t.Fatalf(`platform replies = %#v, want ["escalation ack"]`, sent)
	}
}
