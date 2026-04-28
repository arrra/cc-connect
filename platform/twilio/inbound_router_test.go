package twilio

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// mockSlack is a SlackPoster that records calls for assertions.
type mockSlack struct {
	postMessages []mockSlackMessage
	postReplies  []mockSlackReply
	postErr      error
	replyErr     error
	nextTS       string
}

type mockSlackMessage struct {
	Channel string
	Text    string
}

type mockSlackReply struct {
	Channel  string
	ThreadTS string
	Text     string
}

func (m *mockSlack) PostMessage(_ context.Context, channelID, text string) (string, error) {
	if m.postErr != nil {
		return "", m.postErr
	}
	m.postMessages = append(m.postMessages, mockSlackMessage{Channel: channelID, Text: text})
	ts := m.nextTS
	if ts == "" {
		ts = "1234567890.000001"
	}
	return ts, nil
}

func (m *mockSlack) PostReply(_ context.Context, channelID, threadTS, text string) error {
	if m.replyErr != nil {
		return m.replyErr
	}
	m.postReplies = append(m.postReplies, mockSlackReply{
		Channel:  channelID,
		ThreadTS: threadTS,
		Text:     text,
	})
	return nil
}

func newTestInboundRouter(t *testing.T, slack *mockSlack, store *PhoneThreadStore) *InboundRouter {
	t.Helper()
	adapter := &TwilioAdapter{authToken: "secret"}
	if store == nil {
		store = NewPhoneThreadStore("")
	}
	return NewInboundRouter(adapter, slack, store, "#chief-of-staff")
}

// signedSMSRequest builds a Twilio-signed POST request for the given params and auth token.
func signedSMSRequest(t *testing.T, rawURL string, params url.Values, authToken string) *http.Request {
	t.Helper()
	sig := computeSig(authToken, rawURL, params)
	req := httptest.NewRequest(http.MethodPost, rawURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)
	return req
}

// TestInboundRouter_KnownThread verifies an inbound SMS from a known lead
// is posted as a threaded reply to the existing Slack thread.
func TestInboundRouter_KnownThread(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const phone = "+19165550101"

	slack := &mockSlack{}
	store := NewPhoneThreadStore("")
	_ = store.SetThread(phone, LeadThread{Channel: "C123", ThreadTS: "1111111111.000001"})

	router := newTestInboundRouter(t, slack, store)

	params := smsParams(phone, "SM100", "Hi, I got your SMS")
	req := signedSMSRequest(t, rawURL, params, "secret")
	rr := httptest.NewRecorder()
	router.HandleInbound(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(slack.postReplies) != 1 {
		t.Fatalf("postReplies = %d, want 1", len(slack.postReplies))
	}
	reply := slack.postReplies[0]
	if reply.Channel != "C123" {
		t.Errorf("reply channel = %q, want C123", reply.Channel)
	}
	if reply.ThreadTS != "1111111111.000001" {
		t.Errorf("reply thread_ts = %q, want 1111111111.000001", reply.ThreadTS)
	}
	if reply.Text != "Hi, I got your SMS" {
		t.Errorf("reply text = %q, want 'Hi, I got your SMS'", reply.Text)
	}
	if len(slack.postMessages) != 0 {
		t.Errorf("no new top-level posts expected, got %d", len(slack.postMessages))
	}
}

// TestInboundRouter_OrphanInbound verifies that an inbound SMS from an unknown
// phone creates a top-level Slack post and stores the thread mapping.
func TestInboundRouter_OrphanInbound(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const phone = "+19165550102"

	slack := &mockSlack{nextTS: "9999999999.000001"}
	store := NewPhoneThreadStore("")

	router := newTestInboundRouter(t, slack, store)

	params := smsParams(phone, "SM101", "Hello are you there?")
	req := signedSMSRequest(t, rawURL, params, "secret")
	rr := httptest.NewRecorder()
	router.HandleInbound(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if len(slack.postMessages) != 1 {
		t.Fatalf("postMessages = %d, want 1", len(slack.postMessages))
	}
	msg := slack.postMessages[0]
	if msg.Channel != "#chief-of-staff" {
		t.Errorf("channel = %q, want #chief-of-staff", msg.Channel)
	}
	if !strings.Contains(msg.Text, "orphan inbound") {
		t.Errorf("message text should mention 'orphan inbound', got: %q", msg.Text)
	}
	if !strings.Contains(msg.Text, "Hello are you there?") {
		t.Errorf("message text should include SMS body, got: %q", msg.Text)
	}

	stored, ok := store.GetThread(phone)
	if !ok {
		t.Fatal("thread not stored after orphan inbound")
	}
	if stored.ThreadTS != "9999999999.000001" {
		t.Errorf("stored ThreadTS = %q, want 9999999999.000001", stored.ThreadTS)
	}
	if stored.Channel != "#chief-of-staff" {
		t.Errorf("stored Channel = %q, want #chief-of-staff", stored.Channel)
	}
	if len(slack.postReplies) != 0 {
		t.Errorf("no replies expected for orphan, got %d", len(slack.postReplies))
	}
}

// TestInboundRouter_BadSignature verifies that a request with an invalid
// Twilio signature is rejected with 403 and no Slack posts are made.
func TestInboundRouter_BadSignature(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"

	slack := &mockSlack{}
	router := newTestInboundRouter(t, slack, nil)

	params := smsParams("+19165550103", "SM102", "Hello")
	// Sign with wrong token.
	sig := computeSig("wrong-token", rawURL, params)
	req := httptest.NewRequest(http.MethodPost, rawURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)

	rr := httptest.NewRecorder()
	router.HandleInbound(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if len(slack.postMessages) != 0 || len(slack.postReplies) != 0 {
		t.Error("no Slack messages should be posted for bad signature")
	}
}

// TestInboundRouter_MalformedBody verifies that a request missing required
// Twilio fields is rejected with 403 and no Slack posts are made.
func TestInboundRouter_MalformedBody(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const authToken = "secret"

	slack := &mockSlack{}
	router := newTestInboundRouter(t, slack, nil)

	// Missing From field — adapter.HandleInbound will return an error.
	params := url.Values{
		"MessageSid": {"SM103"},
		"AccountSid": {"ACtest"},
		"Body":       {"hello"},
	}
	sig := computeSig(authToken, rawURL, params)
	req := httptest.NewRequest(http.MethodPost, rawURL, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Twilio-Signature", sig)

	rr := httptest.NewRecorder()
	router.HandleInbound(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rr.Code)
	}
	if len(slack.postMessages) != 0 || len(slack.postReplies) != 0 {
		t.Error("no Slack messages should be posted for malformed body")
	}
}

// TestInboundRouter_SlackPostError verifies that a Slack posting failure
// returns 500 without storing the thread.
func TestInboundRouter_SlackPostError(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const phone = "+19165550104"

	slack := &mockSlack{postErr: fmt.Errorf("slack rate limited")}
	store := NewPhoneThreadStore("")
	router := newTestInboundRouter(t, slack, store)

	params := smsParams(phone, "SM104", "message")
	req := signedSMSRequest(t, rawURL, params, "secret")
	rr := httptest.NewRecorder()
	router.HandleInbound(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	if _, ok := store.GetThread(phone); ok {
		t.Error("thread should not be stored when Slack post fails")
	}
}

// TestInboundRouter_SlackReplyError verifies that a Slack reply failure
// returns 500 for a known lead.
func TestInboundRouter_SlackReplyError(t *testing.T) {
	const rawURL = "https://example.com/twilio/inbound-sms"
	const phone = "+19165550105"

	slack := &mockSlack{replyErr: fmt.Errorf("slack connection timeout")}
	store := NewPhoneThreadStore("")
	_ = store.SetThread(phone, LeadThread{Channel: "C456", ThreadTS: "2222222222.000001"})

	router := newTestInboundRouter(t, slack, store)

	params := smsParams(phone, "SM105", "follow-up message")
	req := signedSMSRequest(t, rawURL, params, "secret")
	rr := httptest.NewRecorder()
	router.HandleInbound(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
}
