package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

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

	sagarCell := os.Getenv("SAGAR_CELL_NUMBER")
	if sagarCell == "" {
		_ = c.Slack.PostReply(ctx, channel, threadTS,
			"⚠️ SAGAR_CELL_NUMBER not configured. Set it in the environment and restart.")
		return fmt.Errorf("!call: SAGAR_CELL_NUMBER not set")
	}

	recordingCB := os.Getenv("RECORDING_STATUS_URL")
	twiml := buildCallTwiML(phone, recordingCB)

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
// California two-party consent: Say preamble appears BEFORE the Dial verb.
func buildCallTwiML(leadPhone, recordingStatusCallback string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString("<Response>")
	sb.WriteString(`<Say voice="alice">This call may be recorded for quality.</Say>`)
	sb.WriteString("<Dial")
	if recordingStatusCallback != "" {
		sb.WriteString(fmt.Sprintf(
			` record="record-from-answer" recordingStatusCallback="%s"`,
			recordingStatusCallback,
		))
	}
	sb.WriteString(">")
	sb.WriteString(fmt.Sprintf("<Number>%s</Number>", leadPhone))
	sb.WriteString("</Dial>")
	sb.WriteString("</Response>")
	return sb.String()
}
