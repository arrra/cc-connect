package commands

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// mockCallInitiator captures calls for assertion in tests.
type mockCallInitiator struct {
	callSid     string
	err         error
	lastToLead  string
	lastTwimlXML string
}

func (m *mockCallInitiator) InitiateCallWithTwiML(toLead, twimlXML string) (string, error) {
	m.lastToLead = toLead
	m.lastTwimlXML = twimlXML
	return m.callSid, m.err
}

func newCallCmd(caller *mockCallInitiator, store *mockThreadLookup, slack *mockSlackReplier) *CallCmd {
	return &CallCmd{
		Twilio: caller,
		Store:  store,
		Slack:  slack,
	}
}

// TestCmdCall_Success verifies the happy path: call initiated, confirmation posted,
// and TwiML contains the two-party consent preamble.
func TestCmdCall_Success(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000010"
	const phone = "+19165550100"
	const sid = "CA0000000011223344"

	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")
	t.Setenv("RECORDING_STATUS_URL", "")

	caller := &mockCallInitiator{callSid: sid}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCallCmd(caller, store, slack)
	if err := cmd.Handle(context.Background(), channel, threadTS, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Correct lead phone passed.
	if caller.lastToLead != phone {
		t.Errorf("toLead: got %q, want %q", caller.lastToLead, phone)
	}

	// TwiML contains the California two-party consent preamble (hard legal requirement).
	if !strings.Contains(caller.lastTwimlXML, "This call may be recorded for quality.") {
		t.Errorf("TwiML missing consent preamble; got:\n%s", caller.lastTwimlXML)
	}

	// TwiML contains the lead's phone number in a <Number> element.
	if !strings.Contains(caller.lastTwimlXML, phone) {
		t.Errorf("TwiML missing lead phone %q; got:\n%s", phone, caller.lastTwimlXML)
	}

	// Say appears before Dial (consent preamble must precede call bridge).
	sayIdx := strings.Index(caller.lastTwimlXML, "<Say")
	dialIdx := strings.Index(caller.lastTwimlXML, "<Dial")
	if sayIdx == -1 || dialIdx == -1 {
		t.Fatalf("TwiML missing <Say> or <Dial>; got:\n%s", caller.lastTwimlXML)
	}
	if sayIdx > dialIdx {
		t.Errorf("<Say> must appear before <Dial> for consent preamble; got:\n%s", caller.lastTwimlXML)
	}

	// Confirmation posted to Slack thread.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slack.replies))
	}
	if slack.replies[0].Channel != channel || slack.replies[0].ThreadTS != threadTS {
		t.Errorf("reply posted to wrong thread")
	}
	if !strings.Contains(slack.replies[0].Text, "📞") {
		t.Errorf("confirmation should contain 📞; got: %q", slack.replies[0].Text)
	}
}

// TestCmdCall_NoThread verifies that !call outside a thread posts usage help.
func TestCmdCall_NoThread(t *testing.T) {
	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")

	caller := &mockCallInitiator{}
	store := &mockThreadLookup{entries: map[string]string{}}
	slack := &mockSlackReplier{}

	cmd := newCallCmd(caller, store, slack)
	if err := cmd.Handle(context.Background(), "C123", "", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No call initiated.
	if caller.lastToLead != "" {
		t.Error("call should not be initiated outside a thread")
	}

	// Usage help posted.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 reply, got %d", len(slack.replies))
	}
	if !strings.Contains(strings.ToLower(slack.replies[0].Text), "usage") {
		t.Errorf("expected usage help, got: %q", slack.replies[0].Text)
	}
}

// TestCmdCall_MissingSagarCell verifies graceful error when SAGAR_CELL_NUMBER is unset.
func TestCmdCall_MissingSagarCell(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000011"
	const phone = "+19165550101"

	t.Setenv("SAGAR_CELL_NUMBER", "")

	caller := &mockCallInitiator{}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCallCmd(caller, store, slack)
	err := cmd.Handle(context.Background(), channel, threadTS, "")
	if err == nil {
		t.Fatal("expected error when SAGAR_CELL_NUMBER is missing")
	}

	// No call initiated.
	if caller.lastToLead != "" {
		t.Error("call should not be initiated when SAGAR_CELL_NUMBER is unset")
	}

	// Error message posted to Slack.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slack.replies))
	}
	if !strings.Contains(slack.replies[0].Text, "SAGAR_CELL_NUMBER") {
		t.Errorf("error reply should mention SAGAR_CELL_NUMBER; got: %q", slack.replies[0].Text)
	}
}

// TestCmdCall_TwilioError verifies that a Twilio failure is surfaced in the thread.
func TestCmdCall_TwilioError(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000012"
	const phone = "+19165550102"

	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")

	twilioErr := errors.New("twilio: http 503")
	caller := &mockCallInitiator{err: twilioErr}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCallCmd(caller, store, slack)
	err := cmd.Handle(context.Background(), channel, threadTS, "")
	if err == nil {
		t.Fatal("expected error from Handle when Twilio fails")
	}
	if !errors.Is(err, twilioErr) {
		t.Errorf("wrong error returned: %v", err)
	}

	// Error reply posted to Slack.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slack.replies))
	}
	if !strings.Contains(slack.replies[0].Text, "Failed to initiate call") {
		t.Errorf("expected failure message; got: %q", slack.replies[0].Text)
	}
}

// TestCmdCall_TwiMLRecordingCallback verifies recording callback URL is embedded when set.
func TestCmdCall_TwiMLRecordingCallback(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000013"
	const phone = "+19165550103"
	const recordURL = "https://worker.example.com/webhooks/twilio/recording-status"

	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")
	t.Setenv("RECORDING_STATUS_URL", recordURL)

	caller := &mockCallInitiator{callSid: "CA999"}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCallCmd(caller, store, slack)
	if err := cmd.Handle(context.Background(), channel, threadTS, ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(caller.lastTwimlXML, recordURL) {
		t.Errorf("TwiML should contain recordingStatusCallback URL; got:\n%s", caller.lastTwimlXML)
	}
	if !strings.Contains(caller.lastTwimlXML, `record="record-from-answer"`) {
		t.Errorf("TwiML should have record attribute; got:\n%s", caller.lastTwimlXML)
	}
}

// TestBuildCallTwiML_PreambleBeforeDial is a focused unit test for the TwiML builder.
func TestBuildCallTwiML_PreambleBeforeDial(t *testing.T) {
	twiml := buildCallTwiML("+19165550123", "")

	const preamble = "This call may be recorded for quality."
	if !strings.Contains(twiml, preamble) {
		t.Errorf("TwiML missing consent preamble %q; got:\n%s", preamble, twiml)
	}

	sayIdx := strings.Index(twiml, "<Say")
	dialIdx := strings.Index(twiml, "<Dial")
	if sayIdx == -1 || dialIdx == -1 {
		t.Fatalf("TwiML missing <Say> or <Dial>")
	}
	if sayIdx > dialIdx {
		t.Errorf("<Say> must come before <Dial>")
	}

	if !strings.Contains(twiml, "alice") {
		t.Errorf("TwiML should use alice voice for preamble; got:\n%s", twiml)
	}
}
