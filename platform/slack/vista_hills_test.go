package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tw "github.com/chenhg5/cc-connect/platform/twilio"
)

// mockVistaSlack records Slack calls made by VistaHillsHandler.
type mockVistaSlack struct {
	messages []struct{ channel, text string }
	replies  []struct{ channel, threadTS, text string }
	postErr  error
	replyErr error
	nextTS   string
}

func (m *mockVistaSlack) PostMessage(_ context.Context, channelID, text string) (string, error) {
	if m.postErr != nil {
		return "", m.postErr
	}
	m.messages = append(m.messages, struct{ channel, text string }{channelID, text})
	ts := m.nextTS
	if ts == "" {
		ts = "1700000000.000001"
	}
	return ts, nil
}

func (m *mockVistaSlack) PostReply(_ context.Context, channelID, threadTS, text string) error {
	if m.replyErr != nil {
		return m.replyErr
	}
	m.replies = append(m.replies, struct{ channel, threadTS, text string }{channelID, threadTS, text})
	return nil
}

func newTestVistaHillsHandler(t *testing.T, slack *mockVistaSlack, store *tw.PhoneThreadStore) *VistaHillsHandler {
	t.Helper()
	if store == nil {
		store = tw.NewPhoneThreadStore("")
	}
	return NewVistaHillsHandler(slack, store, "test-secret", "#chief-of-staff")
}

// postJSON builds a POST request with JSON body and optional secret header.
func postJSON(t *testing.T, path string, body any, secret string) *http.Request {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("X-CC-Connect-Secret", secret)
	}
	return req
}

// TestVistaHills_LeadCreated verifies a valid lead-created request posts to Slack,
// stores the thread mapping, and returns the thread_ts in the response.
func TestVistaHills_LeadCreated(t *testing.T) {
	slack := &mockVistaSlack{nextTS: "1700000001.000001"}
	store := tw.NewPhoneThreadStore("")
	h := newTestVistaHillsHandler(t, slack, store)

	payload := LeadCreatedRequest{
		LeadID:          5871,
		Phone:           "+19165550123",
		Name:            "Mary Chen",
		Source:          "meta",
		Campaign:        "EDH-Brochure-A",
		FormSubmittedAt: "2026-04-28T14:14:08Z",
	}

	req := postJSON(t, "/vista-hills/lead-created", payload, "test-secret")
	rr := httptest.NewRecorder()
	h.HandleLeadCreated(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}

	// Verify Slack post was made to the correct channel.
	if len(slack.messages) != 1 {
		t.Fatalf("slack messages = %d, want 1", len(slack.messages))
	}
	msg := slack.messages[0]
	if msg.channel != "#chief-of-staff" {
		t.Errorf("channel = %q, want #chief-of-staff", msg.channel)
	}
	if !strings.Contains(msg.text, "Mary Chen") {
		t.Errorf("message should contain 'Mary Chen', got: %q", msg.text)
	}
	if !strings.Contains(msg.text, "meta") {
		t.Errorf("message should contain source 'meta', got: %q", msg.text)
	}
	if !strings.Contains(msg.text, "EDH-Brochure-A") {
		t.Errorf("message should contain campaign 'EDH-Brochure-A', got: %q", msg.text)
	}
	if !strings.Contains(msg.text, "AI texting now") {
		t.Errorf("message should contain 'AI texting now', got: %q", msg.text)
	}

	// Verify thread_ts stored for the phone.
	thread, ok := store.GetThread("+19165550123")
	if !ok {
		t.Fatal("thread not stored after lead-created")
	}
	if thread.ThreadTS != "1700000001.000001" {
		t.Errorf("stored ThreadTS = %q, want 1700000001.000001", thread.ThreadTS)
	}

	// Verify response body contains slack_thread_ts.
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["slack_thread_ts"] != "1700000001.000001" {
		t.Errorf("response slack_thread_ts = %q, want 1700000001.000001", resp["slack_thread_ts"])
	}
}

// TestVistaHills_LeadStateUpdate verifies a valid state-update posts a threaded reply.
func TestVistaHills_LeadStateUpdate(t *testing.T) {
	slack := &mockVistaSlack{nextTS: "1700000002.000001"}
	store := tw.NewPhoneThreadStore("")
	h := newTestVistaHillsHandler(t, slack, store)

	// Seed the handler with a known lead.
	_ = store.SetThread("+19165550124", tw.LeadThread{
		Channel:  "#chief-of-staff",
		ThreadTS: "1700000002.000001",
	})
	h.mu.Lock()
	h.leadPhones[100] = "+19165550124"
	h.mu.Unlock()

	payload := LeadStateUpdateRequest{
		LeadID:    100,
		FromState: "new",
		ToState:   "qualified",
		QualifyingData: map[string]any{
			"care_for": "mother",
			"area":     "Folsom",
		},
	}

	req := postJSON(t, "/vista-hills/lead-state-update", payload, "test-secret")
	rr := httptest.NewRecorder()
	h.HandleLeadStateUpdate(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rr.Code, rr.Body.String())
	}
	if len(slack.replies) != 1 {
		t.Fatalf("slack replies = %d, want 1", len(slack.replies))
	}
	reply := slack.replies[0]
	if reply.channel != "#chief-of-staff" {
		t.Errorf("reply channel = %q, want #chief-of-staff", reply.channel)
	}
	if reply.threadTS != "1700000002.000001" {
		t.Errorf("reply threadTS = %q, want 1700000002.000001", reply.threadTS)
	}
	if !strings.Contains(reply.text, "qualified") {
		t.Errorf("reply should mention 'qualified', got: %q", reply.text)
	}
}

// TestVistaHills_BadSecret verifies unauthorized requests are rejected with 401.
func TestVistaHills_BadSecret(t *testing.T) {
	slack := &mockVistaSlack{}
	h := newTestVistaHillsHandler(t, slack, nil)

	payload := LeadCreatedRequest{
		LeadID: 1,
		Phone:  "+19165550199",
		Name:   "Test Lead",
	}

	// No secret header.
	req := postJSON(t, "/vista-hills/lead-created", payload, "")
	rr := httptest.NewRecorder()
	h.HandleLeadCreated(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if len(slack.messages) != 0 {
		t.Error("no Slack messages should be posted for unauthorized request")
	}

	// Wrong secret.
	req2 := postJSON(t, "/vista-hills/lead-created", payload, "wrong-secret")
	rr2 := httptest.NewRecorder()
	h.HandleLeadCreated(rr2, req2)

	if rr2.Code != http.StatusUnauthorized {
		t.Errorf("wrong secret: status = %d, want 401", rr2.Code)
	}
}

// TestVistaHills_MalformedJSON verifies malformed request bodies return 400.
func TestVistaHills_MalformedJSON(t *testing.T) {
	slack := &mockVistaSlack{}
	h := newTestVistaHillsHandler(t, slack, nil)

	req := httptest.NewRequest(http.MethodPost, "/vista-hills/lead-created",
		strings.NewReader("{invalid json}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CC-Connect-Secret", "test-secret")
	rr := httptest.NewRecorder()
	h.HandleLeadCreated(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

// TestVistaHills_StateUpdateIdempotency verifies duplicate state transitions are silently skipped.
func TestVistaHills_StateUpdateIdempotency(t *testing.T) {
	slack := &mockVistaSlack{nextTS: "1700000003.000001"}
	store := tw.NewPhoneThreadStore("")
	h := newTestVistaHillsHandler(t, slack, store)

	_ = store.SetThread("+19165550125", tw.LeadThread{
		Channel:  "#chief-of-staff",
		ThreadTS: "1700000003.000001",
	})
	h.mu.Lock()
	h.leadPhones[200] = "+19165550125"
	h.mu.Unlock()

	payload := LeadStateUpdateRequest{
		LeadID:    200,
		FromState: "new",
		ToState:   "qualified",
	}

	// First call — should post.
	req1 := postJSON(t, "/vista-hills/lead-state-update", payload, "test-secret")
	rr1 := httptest.NewRecorder()
	h.HandleLeadStateUpdate(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first call status = %d, want 200", rr1.Code)
	}

	// Second identical call — should be silently skipped.
	req2 := postJSON(t, "/vista-hills/lead-state-update", payload, "test-secret")
	rr2 := httptest.NewRecorder()
	h.HandleLeadStateUpdate(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("duplicate call status = %d, want 200", rr2.Code)
	}

	if len(slack.replies) != 1 {
		t.Errorf("slack replies = %d after 2 identical requests, want 1 (duplicate skipped)", len(slack.replies))
	}
}

// TestVistaHills_StateUpdateUnknownLead verifies a state-update for an unknown lead returns 404.
func TestVistaHills_StateUpdateUnknownLead(t *testing.T) {
	slack := &mockVistaSlack{}
	h := newTestVistaHillsHandler(t, slack, nil)

	payload := LeadStateUpdateRequest{
		LeadID:    9999,
		FromState: "new",
		ToState:   "qualified",
	}

	req := postJSON(t, "/vista-hills/lead-state-update", payload, "test-secret")
	rr := httptest.NewRecorder()
	h.HandleLeadStateUpdate(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
	if len(slack.replies) != 0 {
		t.Error("no Slack replies expected for unknown lead")
	}
}
