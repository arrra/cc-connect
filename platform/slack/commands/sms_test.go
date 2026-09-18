package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// --- mocks ---

type mockSMSSender struct {
	sid string
	err error
	lastTo   string
	lastBody string
}

func (m *mockSMSSender) SendSMS(to, body string) (string, error) {
	m.lastTo = to
	m.lastBody = body
	return m.sid, m.err
}

type mockThreadLookup struct {
	// phone keyed by "channel:threadTS"
	entries map[string]string
}

func (m *mockThreadLookup) GetPhone(channel, threadTS string) (string, bool) {
	key := channel + ":" + threadTS
	phone, ok := m.entries[key]
	return phone, ok
}

type mockSlackReplier struct {
	replies []mockReply
	err     error
}

type mockReply struct {
	Channel  string
	ThreadTS string
	Text     string
}

func (m *mockSlackReplier) PostReply(_ context.Context, channelID, threadTS, text string) error {
	if m.err != nil {
		return m.err
	}
	m.replies = append(m.replies, mockReply{Channel: channelID, ThreadTS: threadTS, Text: text})
	return nil
}

// newCmd returns a SmsCmd wired with the provided mocks.
func newCmd(sender *mockSMSSender, store *mockThreadLookup, slack *mockSlackReplier) *SmsCmd {
	return &SmsCmd{
		Twilio: sender,
		Store:  store,
		Slack:  slack,
	}
}

// --- tests ---

// TestCmdSms_Success verifies the happy path: SMS sent, confirmation posted.
func TestCmdSms_Success(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000001"
	const phone = "+19165550100"
	const body = "Hello Mary"
	const sid = "SM0000000011223344"

	sender := &mockSMSSender{sid: sid}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCmd(sender, store, slack)
	if err := cmd.Handle(context.Background(), channel, threadTS, body); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SMS was sent to the right number with the right body.
	if sender.lastTo != phone {
		t.Errorf("SMS to: got %q, want %q", sender.lastTo, phone)
	}
	if sender.lastBody != body {
		t.Errorf("SMS body: got %q, want %q", sender.lastBody, body)
	}

	// Confirmation posted to the same thread.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slack.replies))
	}
	reply := slack.replies[0]
	if reply.Channel != channel || reply.ThreadTS != threadTS {
		t.Errorf("reply posted to wrong thread: channel=%q threadTS=%q", reply.Channel, reply.ThreadTS)
	}
	if !strings.Contains(reply.Text, "✓ SMS sent") {
		t.Errorf("confirmation text missing '✓ SMS sent': %q", reply.Text)
	}
	if !strings.Contains(reply.Text, sid[:8]) {
		t.Errorf("confirmation text missing SID prefix: %q", reply.Text)
	}
}

// TestCmdSms_NoThread verifies that using !sms outside a thread returns usage help.
func TestCmdSms_NoThread(t *testing.T) {
	const channel = "C123"

	sender := &mockSMSSender{}
	store := &mockThreadLookup{entries: map[string]string{}}
	slack := &mockSlackReplier{}

	cmd := newCmd(sender, store, slack)
	// threadTS is empty — message sent in main channel.
	if err := cmd.Handle(context.Background(), channel, "", "Hello"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SMS must NOT have been sent.
	if sender.lastTo != "" {
		t.Error("SMS should not have been sent outside a thread")
	}

	// Usage help should be posted.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 reply (usage), got %d", len(slack.replies))
	}
	if !strings.Contains(strings.ToLower(slack.replies[0].Text), "usage") {
		t.Errorf("expected usage help, got: %q", slack.replies[0].Text)
	}
}

// TestCmdSms_TooLong verifies that messages exceeding 1500 chars are rejected.
func TestCmdSms_TooLong(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000002"
	const phone = "+19165550101"

	sender := &mockSMSSender{sid: "SMxxxx"}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	longText := strings.Repeat("x", maxSMSLen+1)
	cmd := newCmd(sender, store, slack)
	if err := cmd.Handle(context.Background(), channel, threadTS, longText); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// SMS must NOT have been sent.
	if sender.lastTo != "" {
		t.Error("SMS should not be sent when text is too long")
	}

	// Error message posted to thread.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(slack.replies))
	}
	if !strings.Contains(slack.replies[0].Text, "too long") {
		t.Errorf("expected 'too long' in reply, got: %q", slack.replies[0].Text)
	}
}

// TestCmdSms_TwilioError verifies that a Twilio failure is surfaced in the thread.
func TestCmdSms_TwilioError(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000003"
	const phone = "+19165550102"

	twilioErr := errors.New("twilio: http 503")
	sender := &mockSMSSender{err: twilioErr}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCmd(sender, store, slack)
	err := cmd.Handle(context.Background(), channel, threadTS, "Hello")
	if err == nil {
		t.Fatal("expected error from Handle when Twilio fails")
	}
	if !errors.Is(err, twilioErr) {
		t.Errorf("wrong error returned: %v", err)
	}

	// Error reply posted to thread.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slack.replies))
	}
	if !strings.Contains(slack.replies[0].Text, "Failed to send SMS") {
		t.Errorf("expected failure message, got: %q", slack.replies[0].Text)
	}
}

// TestCmdSms_MissingFromNumber verifies graceful handling when Twilio errors with
// a missing TWILIO_FROM_NUMBER (adapter not fully configured).
func TestCmdSms_MissingFromNumber(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000004"
	const phone = "+19165550103"

	fromErr := errors.New("twilio: TWILIO_FROM_NUMBER not set")
	sender := &mockSMSSender{err: fromErr}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCmd(sender, store, slack)
	err := cmd.Handle(context.Background(), channel, threadTS, "Test message")
	if err == nil {
		t.Fatal("expected error when from-number is missing")
	}

	// Error reply contains actionable information.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slack.replies))
	}
	if !strings.Contains(slack.replies[0].Text, "Failed to send SMS") {
		t.Errorf("expected failure message, got: %q", slack.replies[0].Text)
	}
}

// TestFormatPhone checks the human-readable phone formatter.
func TestFormatPhone(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"+19165550123", "(916) 555-0123"},
		{"+15305550199", "(530) 555-0199"},
		{"+441234567890", "+441234567890"}, // non-US: fall back to raw
		{"short", "short"},
	}
	for _, tc := range cases {
		if got := FormatPhone(tc.input); got != tc.want {
			t.Errorf("FormatPhone(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMaskPhone_Sms(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"+19165550123", "+19*****0123"},
		{"short", "***"},
	}
	for _, tc := range cases {
		if got := maskPhone(tc.input); got != tc.want {
			t.Errorf("maskPhone(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestPersistOutbound_BestEffort confirms persistOutbound doesn't panic on bad URLs.
func TestPersistOutbound_BestEffort(t *testing.T) {
	// Invalid URL — must not panic.
	persistOutbound("://bad-url", "+19165550100", "hello", "SMxxx")
	// Connection refused — must not panic.
	persistOutbound("http://127.0.0.1:1", "+19165550100", "hello", "SMyyy")
}
