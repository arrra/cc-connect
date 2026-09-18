package twilio

import "strings"

// EscapeSlackText escapes user-supplied text before posting to Slack to prevent
// mention injection (<@U123>, <!channel>, <#C123>) and markdown rendering.
// Follows Slack's documented escaping rules: https://api.slack.com/reference/surfaces/formatting#escaping
func EscapeSlackText(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}
