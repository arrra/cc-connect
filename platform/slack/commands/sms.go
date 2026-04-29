package commands

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const maxSMSLen = 1500

// SMSSender sends an outbound SMS.
type SMSSender interface {
	SendSMS(to, body string) (sid string, err error)
}

// ThreadLookup resolves a Slack (channel, threadTS) pair to a lead phone number.
type ThreadLookup interface {
	GetPhone(channel, threadTS string) (string, bool)
}

// SlackReplier posts messages to a Slack thread.
type SlackReplier interface {
	PostReply(ctx context.Context, channelID, threadTS, text string) error
}

// SmsCmd handles the !sms slash command typed in a Slack lead thread.
type SmsCmd struct {
	Twilio           SMSSender
	Store            ThreadLookup
	Slack            SlackReplier
	WorkerWebhookURL string // POST /webhooks/cc-connect/outbound-sms; optional
}

// Handle processes a "!sms <text>" command from a Slack thread.
// channel is the Slack channel ID; threadTS is the parent thread timestamp
// (empty when the message was sent outside any thread).
func (c *SmsCmd) Handle(ctx context.Context, channel, threadTS, args string) error {
	text := strings.TrimSpace(args)

	// Sent outside a lead thread — return usage help in a new message.
	if threadTS == "" {
		return c.Slack.PostReply(ctx, channel, "",
			"Usage: `!sms <message>` — must be used inside a lead thread.")
	}

	// Validate text before doing any lookups.
	if text == "" {
		return c.Slack.PostReply(ctx, channel, threadTS,
			"Usage: `!sms <message>` — message cannot be empty.")
	}
	if len(text) > maxSMSLen {
		return c.Slack.PostReply(ctx, channel, threadTS,
			fmt.Sprintf("⚠️ Message too long (%d chars). SMS must be ≤%d characters.", len(text), maxSMSLen))
	}

	// Resolve lead phone from thread mapping.
	phone, ok := c.Store.GetPhone(channel, threadTS)
	if !ok {
		return c.Slack.PostReply(ctx, channel, threadTS,
			"⚠️ No lead found for this thread. Cannot send SMS.")
	}

	// Send the SMS.
	sid, err := c.Twilio.SendSMS(phone, text)
	if err != nil {
		slog.Error("[!sms] send failed",
			"lead_phone", maskPhone(phone),
			"length", len(text),
			"error", err,
		)
		_ = c.Slack.PostReply(ctx, channel, threadTS,
			fmt.Sprintf("❌ Failed to send SMS: %v\n\nCheck Twilio credentials and try again.", err))
		return err
	}

	slog.Info("[!sms]",
		"lead_phone", maskPhone(phone),
		"length", len(text),
		"sid", sid,
	)

	// Post confirmation with human-readable phone and truncated SID.
	shortSID := sid
	if len(shortSID) > 8 {
		shortSID = shortSID[:8] + "…"
	}
	if err := c.Slack.PostReply(ctx, channel, threadTS,
		fmt.Sprintf("✓ SMS sent via %s [sid: %s]", FormatPhone(phone), shortSID),
	); err != nil {
		slog.Warn("[!sms] could not post confirmation", "error", err)
	}

	// Persist outbound to Worker webhook (best-effort, non-blocking).
	if c.WorkerWebhookURL != "" {
		go persistOutbound(c.WorkerWebhookURL, phone, text, sid)
	}

	return nil
}

// FormatPhone formats an E.164 number as (XXX) XXX-XXXX.
// Falls back to the raw number if it can't be parsed.
func FormatPhone(phone string) string {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)

	// Strip leading country code 1 for US/CA numbers.
	if len(digits) == 11 && digits[0] == '1' {
		digits = digits[1:]
	}
	if len(digits) == 10 {
		return fmt.Sprintf("(%s) %s-%s", digits[0:3], digits[3:6], digits[6:10])
	}
	return phone
}

// maskPhone masks the middle digits of a phone number for safe logging.
func maskPhone(phone string) string {
	if len(phone) < 8 {
		return "***"
	}
	return phone[:3] + strings.Repeat("*", len(phone)-7) + phone[len(phone)-4:]
}

// persistOutbound POSTs the outbound SMS event to the Worker's webhook (best-effort).
func persistOutbound(webhookURL, phone, body, sid string) {
	form := url.Values{
		"phone": {phone},
		"body":  {body},
		"sid":   {sid},
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodPost, webhookURL, strings.NewReader(form.Encode()))
	if err != nil {
		slog.Warn("[!sms] persist: build request failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if secret := os.Getenv("CC_CONNECT_WEBHOOK_SECRET"); secret != "" {
		req.Header.Set("X-CC-Connect-Secret", secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("[!sms] persist: webhook failed", "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		slog.Warn("[!sms] persist: webhook returned error", "status", resp.StatusCode)
	}
}
