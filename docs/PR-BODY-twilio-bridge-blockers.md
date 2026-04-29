## Summary

This PR supersedes `arrra/cc-connect#13` (Twilio bridge). It addresses **9 cross-confirmed blockers** surfaced by a multi-reviewer audit (Opus 4.7, gpt-5, o3, ultrareview) before any production traffic is routed through the bridge.

---

## Blockers Fixed

### CRITICAL — legal / security exposure

| Task | Finding | Source |
|------|---------|--------|
| t-2 | **Dead code**: Twilio bridge was never wired into `main.go` — no HTTP routes registered, no slash commands active | ultrareview |
| t-3 | **CA Penal Code § 632**: Two-party consent preamble only played to agent leg; lead never heard the disclosure. Fixed via `<Number url=...>` lead-preamble TwiML + new `/twilio/lead-preamble` endpoint | gpt-5 |
| t-5 | **Fail-open auth**: `NewVistaHillsHandler` accepted empty secret silently; `authenticate()` returned `true` unconditionally | Opus + ultrareview + o3 |
| t-4 | **Signature bypass**: `verifyTwilioSignature` only appended first value for repeated POST params — broken for MMS payloads with `MediaUrl0`/`MediaUrl1` | gpt-5 |
| t-6 | **TwiML injection**: `leadPhone` interpolated without E.164 validation or XML escaping into `<Number>` element | Opus + ultrareview + o3 |

### HARDENING — pre-merge

| Task | Finding | Source |
|------|---------|--------|
| t-7 | `RECORDING_STATUS_URL` unset silently disabled recording with no operator warning; `!call` now refuses and posts Slack feedback | Opus |
| t-8 | `PhoneThreadStore` wrote PII (lead E.164 phones) at 0644 (world-readable); fixed to 0600 file / 0700 dir | o3 |
| t-9 | Inbound SMS body posted to Slack without escaping — `<@channel>` injection possible; `escapeSlackText()` now applied | ultrareview |
| t-10 | `SLACK_LEADS_CHANNEL` defaulted to hardcoded `#chief-of-staff`; constructor now returns error if unset | o3 |

---

## Coverage

| Package | Before | After |
|---------|--------|-------|
| `platform/twilio` | ~60% (est.) | **85.1%** |
| `platform/slack/commands` | ~65% (est.) | **90.0%** |
| `platform/slack/vista_hills.go` | ~70% (est.) | **86.1%** (avg per function) |

---

## Out of Scope (follow-up queue)

- Lead phone mapping persistence (in-memory acceptable for Phase 1)
- `lastTurnInputTokens` race, `WireV1Store`, `applyAutoRecall` hex search timeout (pre-existing, not PR #13 scope)
- 5xx retry sleep, doc env-var alignment

---

## Test Plan

- [x] `go build ./...` exits 0
- [x] `go vet ./...` clean
- [x] `go test ./... -count=1 -timeout 120s` — all packages pass (pre-existing `agent/codex` flake unrelated to this PR, confirmed failing on base branch too)
- [x] Unit test: `NewVistaHillsHandler("")` returns error
- [x] Unit test: `verifyTwilioSignature` with multi-valued params (`MediaUrl` × 2) passes; first-value-only signature rejected
- [x] Unit test: `buildCallTwiML` contains `<Number url="...">` with preamble URL and `answerOnBridge="true"`
- [x] Unit test: `!call` with `RECORDING_STATUS_URL` unset returns Slack feedback, no Twilio call placed
- [x] Unit test: `!call` with invalid phone (`<>&"`) rejected before TwiML construction
- [x] Unit test: `PhoneThreadStore` file 0600, dir 0700 after write
- [x] Unit test: `escapeSlackText("<@U123>")` → escaped, `<!channel>` does not ping
- [x] Unit test: `NewInboundRouter` with empty channel and `SLACK_LEADS_CHANNEL` unset returns error

---

## Recommendation

Close `arrra/cc-connect#13` in favor of this branch. The original PR is a dead-code stub; this PR is the complete, audited implementation ready for staging traffic.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
