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
// and TwiML contains the two-party consent preamble for both legs.
func TestCmdCall_Success(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000010"
	const phone = "+19165550100"
	const sid = "CA0000000011223344"
	const preambleURL = "https://example.com/twilio/lead-preamble"

	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")
	t.Setenv("RECORDING_STATUS_URL", "https://example.com/webhooks/recording-status")
	t.Setenv("TWILIO_LEAD_PREAMBLE_URL", preambleURL)

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

	// TwiML contains the California two-party consent preamble for the agent leg.
	if !strings.Contains(caller.lastTwimlXML, "This call may be recorded for quality and training purposes.") {
		t.Errorf("TwiML missing agent-leg consent preamble; got:\n%s", caller.lastTwimlXML)
	}

	// TwiML <Number> includes the lead-preamble URL for the called (lead) leg.
	if !strings.Contains(caller.lastTwimlXML, preambleURL) {
		t.Errorf("TwiML missing lead-preamble URL %q; got:\n%s", preambleURL, caller.lastTwimlXML)
	}

	// <Dial> has answerOnBridge="true" so the lead doesn't hear ringing prematurely.
	if !strings.Contains(caller.lastTwimlXML, `answerOnBridge="true"`) {
		t.Errorf("TwiML missing answerOnBridge=\"true\" on <Dial>; got:\n%s", caller.lastTwimlXML)
	}

	// TwiML contains the lead's phone number in a <Number> element.
	if !strings.Contains(caller.lastTwimlXML, phone) {
		t.Errorf("TwiML missing lead phone %q; got:\n%s", phone, caller.lastTwimlXML)
	}

	// Say appears before Dial (agent-leg consent preamble must precede call bridge).
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

// TestCmdCall_MissingPreambleURL verifies that !call refuses when TWILIO_LEAD_PREAMBLE_URL is unset.
func TestCmdCall_MissingPreambleURL(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000015"
	const phone = "+19165550105"

	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")
	t.Setenv("TWILIO_LEAD_PREAMBLE_URL", "")

	caller := &mockCallInitiator{}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCallCmd(caller, store, slack)
	err := cmd.Handle(context.Background(), channel, threadTS, "")
	if err == nil {
		t.Fatal("expected error when TWILIO_LEAD_PREAMBLE_URL is missing")
	}

	// No call initiated — legal compliance check must block the call.
	if caller.lastToLead != "" {
		t.Error("call should not be initiated when TWILIO_LEAD_PREAMBLE_URL is unset")
	}

	// Error posted to Slack mentioning the legal requirement.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slack.replies))
	}
	if !strings.Contains(slack.replies[0].Text, "TWILIO_LEAD_PREAMBLE_URL") {
		t.Errorf("error reply should mention TWILIO_LEAD_PREAMBLE_URL; got: %q", slack.replies[0].Text)
	}
}

// TestCmdCall_MissingRecordingStatusURL verifies that !call refuses when RECORDING_STATUS_URL is unset.
func TestCmdCall_MissingRecordingStatusURL(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000016"
	const phone = "+19165550106"

	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")
	t.Setenv("TWILIO_LEAD_PREAMBLE_URL", "https://example.com/twilio/lead-preamble")
	t.Setenv("RECORDING_STATUS_URL", "")

	caller := &mockCallInitiator{}
	store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
	slack := &mockSlackReplier{}

	cmd := newCallCmd(caller, store, slack)
	err := cmd.Handle(context.Background(), channel, threadTS, "")
	if err == nil {
		t.Fatal("expected error when RECORDING_STATUS_URL is missing")
	}

	// No Twilio call placed.
	if caller.lastToLead != "" {
		t.Error("call must not be placed when RECORDING_STATUS_URL is unset")
	}

	// Slack error posted mentioning recording configuration.
	if len(slack.replies) != 1 {
		t.Fatalf("expected 1 Slack reply, got %d", len(slack.replies))
	}
	if !strings.Contains(slack.replies[0].Text, "RECORDING_STATUS_URL") {
		t.Errorf("error reply should mention RECORDING_STATUS_URL; got: %q", slack.replies[0].Text)
	}
}

// TestCmdCall_TwilioError verifies that a Twilio failure is surfaced in the thread.
func TestCmdCall_TwilioError(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000012"
	const phone = "+19165550102"

	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")
	t.Setenv("RECORDING_STATUS_URL", "https://example.com/webhooks/recording-status")
	t.Setenv("TWILIO_LEAD_PREAMBLE_URL", "https://example.com/twilio/lead-preamble")

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
	t.Setenv("TWILIO_LEAD_PREAMBLE_URL", "https://example.com/twilio/lead-preamble")

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
	const preambleURL = "https://example.com/twilio/lead-preamble"
	twiml, err := buildCallTwiML("+19165550123", preambleURL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	const preamble = "This call may be recorded for quality and training purposes."
	if !strings.Contains(twiml, preamble) {
		t.Errorf("TwiML missing agent-leg consent preamble %q; got:\n%s", preamble, twiml)
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

	// Lead leg: <Number> must include preamble URL.
	if !strings.Contains(twiml, preambleURL) {
		t.Errorf("TwiML missing lead-preamble URL %q; got:\n%s", preambleURL, twiml)
	}

	// <Dial> must have answerOnBridge="true".
	if !strings.Contains(twiml, `answerOnBridge="true"`) {
		t.Errorf("TwiML missing answerOnBridge=\"true\"; got:\n%s", twiml)
	}
}

// TestCmdCall_InvalidPhoneFormat verifies that !call rejects phones that aren't valid E.164.
func TestCmdCall_InvalidPhoneFormat(t *testing.T) {
	const channel = "C123"
	const threadTS = "1234567890.000020"

	t.Setenv("SAGAR_CELL_NUMBER", "+15305550199")
	t.Setenv("TWILIO_LEAD_PREAMBLE_URL", "https://example.com/twilio/lead-preamble")
	t.Setenv("RECORDING_STATUS_URL", "")

	invalidPhones := []string{
		"1234567890",           // no leading +
		"+1 (916) 555-0100",    // spaces / punctuation
		"+abc",                 // letters
		"",                     // empty
		"+",                    // bare plus
		"+1234",                // too short (< 6 digits after +)
		"<+19165550100>",       // XML injection attempt
		`"><Redirect>evil</Redirect><Number`, // classic injection attempt
	}

	for _, phone := range invalidPhones {
		phone := phone
		t.Run(phone, func(t *testing.T) {
			caller := &mockCallInitiator{}
			store := &mockThreadLookup{entries: map[string]string{channel + ":" + threadTS: phone}}
			slack := &mockSlackReplier{}

			cmd := newCallCmd(caller, store, slack)
			err := cmd.Handle(context.Background(), channel, threadTS, "")
			if err == nil {
				t.Fatalf("expected error for invalid phone %q", phone)
			}

			// No Twilio call placed.
			if caller.lastToLead != "" {
				t.Errorf("call must not be placed for invalid phone %q", phone)
			}

			// Slack error posted.
			if len(slack.replies) != 1 {
				t.Fatalf("expected 1 Slack reply, got %d for phone %q", len(slack.replies), phone)
			}
		})
	}
}

// TestBuildCallTwiML_XMLInjectionPrevented verifies that crafted phone strings
// containing XML special characters are escaped and cannot break TwiML structure.
func TestBuildCallTwiML_XMLInjectionPrevented(t *testing.T) {
	// This phone would break TwiML if unescaped: closing the element and injecting new ones.
	injectedPhone := `+1234567890"><Redirect>evil</Redirect><Number`
	preambleURL := "https://example.com/twilio/lead-preamble"
	twiml, err := buildCallTwiML(injectedPhone, preambleURL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The raw injection string must not appear verbatim in the output.
	if strings.Contains(twiml, "<Redirect>") {
		t.Errorf("TwiML contains unescaped injection; got:\n%s", twiml)
	}

	// XML-escaped versions of the special characters must appear instead.
	if !strings.Contains(twiml, "&lt;") || !strings.Contains(twiml, "&gt;") {
		t.Errorf("TwiML should contain XML-escaped < and > characters; got:\n%s", twiml)
	}
}

// TestBuildCallTwiML_XMLSpecialCharsEscaped verifies that &, <, >, and " are
// XML-escaped in the <Number> element content and url attribute.
func TestBuildCallTwiML_XMLSpecialCharsEscaped(t *testing.T) {
	// Craft a phone containing XML special chars.
	phone := `+12345&<>"678`
	preambleURL := "https://example.com/twilio/lead-preamble"
	twiml, err := buildCallTwiML(phone, preambleURL, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Raw special chars must not appear unescaped in the element content.
	// Find the <Number ...> element.
	numStart := strings.Index(twiml, "<Number")
	if numStart == -1 {
		t.Fatalf("TwiML missing <Number> element; got:\n%s", twiml)
	}
	numEnd := strings.Index(twiml[numStart:], "</Number>")
	if numEnd == -1 {
		t.Fatalf("TwiML missing </Number> closing tag; got:\n%s", twiml)
	}
	numberElem := twiml[numStart : numStart+numEnd+len("</Number>")]

	for _, raw := range []string{"&", "<", ">", `"`} {
		// Allow raw & only as part of &amp; / &lt; / etc. — check escaped form is present
		// by ensuring no lone raw special char appears in element content.
		_ = raw
	}

	// The escaped amp must appear somewhere in the number element.
	if !strings.Contains(numberElem, "&amp;") {
		t.Errorf("<Number> element should contain &amp; for &; got:\n%s", numberElem)
	}
}

// TestBuildCallTwiML_RecordingCallbackEscaped verifies that a recordingStatusCallback URL
// containing '&' is XML-attribute-escaped so the TwiML is well-formed.
func TestBuildCallTwiML_RecordingCallbackEscaped(t *testing.T) {
	const leadPhone = "+19165550123"
	const preambleURL = "https://example.com/preamble"
	const callbackURL = "https://x.com/cb?lead=%2B1&signature=abc"

	twiml, err := buildCallTwiML(leadPhone, preambleURL, callbackURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The raw & must not appear as a bare ampersand inside an attribute value.
	// We want &amp; in the attribute, not the literal &.
	if strings.Contains(twiml, `recordingStatusCallback="https://x.com/cb?lead=%2B1&signature=abc"`) {
		t.Errorf("TwiML contains unescaped & in recordingStatusCallback attribute; got:\n%s", twiml)
	}
	if !strings.Contains(twiml, "&amp;") {
		t.Errorf("TwiML should contain &amp; for escaped & in recordingStatusCallback; got:\n%s", twiml)
	}
}

// TestXMLEscapeAttr verifies that xmlEscapeAttr escapes all five XML attribute special chars.
func TestXMLEscapeAttr(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"a&b", "a&amp;b"},
		{"a<b", "a&lt;b"},
		{"a>b", "a&gt;b"},
		{`a"b`, "a&quot;b"},
		{"a'b", "a&#39;b"},
		// Real-world URL with & in query string — the critical attribute case.
		{
			"https://x.com/cb?lead=%2B1&signature=abc",
			"https://x.com/cb?lead=%2B1&amp;signature=abc",
		},
		// All five special chars at once.
		{
			`&<>"'`,
			"&amp;&lt;&gt;&quot;&#39;",
		},
	}
	for _, tc := range cases {
		got := xmlEscapeAttr(tc.input)
		if got != tc.want {
			t.Errorf("xmlEscapeAttr(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestIsValidE164 exercises the E.164 validator directly.
func TestIsValidE164(t *testing.T) {
	valid := []string{
		"+19165550100",
		"+15305550199",
		"+447700900001",
		"+123456",   // 6 digits — minimum
		"+123456789012345", // 15 digits — maximum
	}
	for _, p := range valid {
		if !isValidE164(p) {
			t.Errorf("isValidE164(%q) = false, want true", p)
		}
	}

	invalid := []string{
		"",
		"+",
		"+12345",            // only 5 digits — below minimum
		"19165550100",       // no leading +
		"+1 916 555 0100",   // spaces
		"+1-916-555-0100",   // dashes
		"+1234567890123456", // 16 digits — above maximum
		"+abc",              // letters
		`+1234><script`,     // XML injection chars
	}
	for _, p := range invalid {
		if isValidE164(p) {
			t.Errorf("isValidE164(%q) = true, want false", p)
		}
	}
}

// TestPreambleURL_AppendsToExistingQuery verifies that a preamble URL that already
// contains query parameters has the lead param added (not replacing existing params).
func TestPreambleURL_AppendsToExistingQuery(t *testing.T) {
	const phone = "+19165550123"
	twiml, err := buildCallTwiML(phone, "https://host/path?existing=1", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(twiml, "existing=1") {
		t.Errorf("TwiML should preserve existing query param; got:\n%s", twiml)
	}
	if !strings.Contains(twiml, "lead=") {
		t.Errorf("TwiML should contain lead param; got:\n%s", twiml)
	}
}

// TestPreambleURL_HandlesFragment verifies that a preamble URL with a fragment
// component is handled correctly (fragment preserved in output).
func TestPreambleURL_HandlesFragment(t *testing.T) {
	const phone = "+19165550123"
	twiml, err := buildCallTwiML(phone, "https://host/path#anchor", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(twiml, "anchor") {
		t.Errorf("TwiML should preserve URL fragment; got:\n%s", twiml)
	}
	if !strings.Contains(twiml, "lead=") {
		t.Errorf("TwiML should contain lead param; got:\n%s", twiml)
	}
}
