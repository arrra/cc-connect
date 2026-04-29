package commands

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// e164Re matches valid E.164 phone numbers: + followed by 6–15 digits.
var e164Re = regexp.MustCompile(`^\+[0-9]{6,15}$`)

// isValidE164 reports whether s is a valid E.164 phone number.
func isValidE164(s string) bool { return e164Re.MatchString(s) }

// xmlEscape XML-encodes s so it is safe to embed in XML element content or attributes.
func xmlEscape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

// CallInitiator initiates a click-to-call with inline TwiML.
// The twimlXML parameter contains the full TwiML document (not a URL),
// so the two-party consent preamble is embedded in the Twilio API request body.
type CallInitiator interface {
	InitiateCallWithTwiML(toLead, twimlXML string) (callSid string, err error)
}

// CallCmd handles the !call slash command typed in a Slack lead thread.
type CallCmd struct {
	Twilio CallInitiator
	Store  ThreadLookup
	Slack  SlackReplier
}

// Handle processes a "!call" command from a Slack thread.
func (c *CallCmd) Handle(ctx context.Context, channel, threadTS, args string) error {
	if threadTS == "" {
		return c.Slack.PostReply(ctx, channel, "",
			"Usage: `!call` — must be used inside a lead thread.")
	}

	phone, ok := c.Store.GetPhone(channel, threadTS)
	if !ok {
		return c.Slack.PostReply(ctx, channel, threadTS,
			"⚠️ No lead found for this thread. Cannot initiate call.")
	}

	if !isValidE164(phone) {
		_ = c.Slack.PostReply(ctx, channel, threadTS,
			fmt.Sprintf("⚠️ Lead phone number %q is not a valid E.164 number (+digits). Cannot initiate call.", phone))
		return fmt.Errorf("!call: invalid lead phone %q", phone)
	}

	sagarCell := os.Getenv("SAGAR_CELL_NUMBER")
	if sagarCell == "" {
		_ = c.Slack.PostReply(ctx, channel, threadTS,
			"⚠️ SAGAR_CELL_NUMBER not configured. Set it in the environment and restart.")
		return fmt.Errorf("!call: SAGAR_CELL_NUMBER not set")
	}

	// California Penal Code § 632 requires both parties hear the recording disclosure.
	// TWILIO_LEAD_PREAMBLE_URL serves TwiML to the lead leg; without it the call is non-compliant.
	preambleURL := os.Getenv("TWILIO_LEAD_PREAMBLE_URL")
	if preambleURL == "" {
		_ = c.Slack.PostReply(ctx, channel, threadTS,
			"⚠️ TWILIO_LEAD_PREAMBLE_URL not configured. Two-party consent (CA Penal Code § 632) requires the lead hear the recording disclosure. Set the env var and restart.")
		return fmt.Errorf("!call: TWILIO_LEAD_PREAMBLE_URL not set")
	}

	recordingCB := os.Getenv("RECORDING_STATUS_URL")
	if recordingCB == "" {
		_ = c.Slack.PostReply(ctx, channel, threadTS,
			"⚠️ Recording is disabled (RECORDING_STATUS_URL not configured). Set the env var to enable recording before placing calls.")
		return fmt.Errorf("!call: RECORDING_STATUS_URL not set")
	}
	twiml := buildCallTwiML(phone, preambleURL, recordingCB)

	callSid, err := c.Twilio.InitiateCallWithTwiML(phone, twiml)
	if err != nil {
		slog.Error("[!call] initiate failed",
			"lead_phone", maskPhone(phone),
			"sagar", maskPhone(sagarCell),
			"error", err,
		)
		_ = c.Slack.PostReply(ctx, channel, threadTS,
			fmt.Sprintf("❌ Failed to initiate call: %v\n\nCheck Twilio credentials and try again.", err))
		return err
	}

	slog.Info("[!call]",
		"lead_phone", maskPhone(phone),
		"sagar", maskPhone(sagarCell),
		"call_sid", callSid,
	)

	msg := fmt.Sprintf("📞 Calling lead at %s from %s — your phone will ring shortly.",
		FormatPhone(phone), FormatPhone(sagarCell))
	if err := c.Slack.PostReply(ctx, channel, threadTS, msg); err != nil {
		slog.Warn("[!call] could not post confirmation", "error", err)
	}

	return nil
}

// buildCallTwiML returns inline TwiML for a click-to-call bridge.
// California two-party consent (CA Penal Code § 632):
//   - Calling leg (agent) hears the <Say> preamble before <Dial>.
//   - Called leg (lead) hears the preamble served by preambleURL via <Number url="...">.
//   - answerOnBridge="true" prevents the lead from hearing ringing audio prematurely.
func buildCallTwiML(leadPhone, preambleURL, recordingStatusCallback string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString("<Response>")
	sb.WriteString(`<Say voice="alice">This call may be recorded for quality and training purposes.</Say>`)
	sb.WriteString(`<Dial answerOnBridge="true"`)
	if recordingStatusCallback != "" {
		sb.WriteString(fmt.Sprintf(
			` record="record-from-answer" recordingStatusCallback="%s"`,
			recordingStatusCallback,
		))
	}
	sb.WriteString(">")
	numberURL := preambleURL + "?lead=" + url.QueryEscape(leadPhone)
	// XML-escape both the attribute value and element content as defense in depth.
	sb.WriteString(fmt.Sprintf(`<Number url="%s">%s</Number>`, xmlEscape(numberURL), xmlEscape(leadPhone)))
	sb.WriteString("</Dial>")
	sb.WriteString("</Response>")
	return sb.String()
}
