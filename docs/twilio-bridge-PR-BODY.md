# feat(platform/twilio): Vista Hills SMS+voice bridge

Adds a complete Twilio adapter to cc-connect so Sagar can manage Vista Hills leads from Slack `#chief-of-staff`: receive inbound SMS as thread replies, send outbound SMS with `!sms`, and place click-to-call bridges with `!call`.

## What's in this PR

### New package: `platform/twilio/`

| File | Purpose |
|------|---------|
| `twilio.go` | `TwilioAdapter` — `SendSMS`, `InitiateCall`, `InitiateCallWithTwiML`, `HandleInbound` (signature verification) |
| `client.go` | HTTP client with Basic auth (no Twilio SDK) |
| `types.go` | Message, Call, Inbound structs + error types |
| `thread_store.go` | `PhoneThreadStore` — phone → Slack thread_ts mapping (JSON-persisted, atomic writes) |
| `inbound_router.go` | `InboundRouter` — POST /twilio/inbound-sms → Slack thread reply or orphan post |
| `*_test.go` | Unit tests (SendSMS retries, signature verification, inbound routing) |

### New slash commands: `platform/slack/commands/`

| Command | Behavior |
|---------|---------|
| `!sms <text>` | Sends outbound SMS to lead phone, posts confirmation in thread |
| `!call` | Initiates click-to-call bridge; Twilio rings Sagar's cell, bridges to lead with CA two-party consent preamble |

### New webhook handlers: `platform/slack/vista_hills.go`

| Endpoint | Triggered by |
|----------|-------------|
| `POST /vista-hills/lead-created` | Worker creates new lead → Slack top-level post + thread mapping |
| `POST /vista-hills/lead-state-update` | Lead state transition → Slack thread reply |

### Integration tests: `tests/integration/twilio-bridge_test.go`

All 4 scenarios tested with mocked Slack + mocked Twilio (no real API calls, runs in CI):

1. Outbound SMS: `!sms hello mary` → correct Twilio call, thread confirmation
2. Inbound SMS: Twilio-signed webhook → known thread reply / orphan top-level post
3. `!call`: TwiML contains consent preamble before `<Dial>`, Slack confirmation posted
4. Lead state update: lead-created + state-update → thread reply with transition text

### Docs: `docs/twilio-bridge-manual-smoke.md`

Step-by-step smoke procedure for Sagar to run post-deploy with real Twilio credentials.

## Key design decisions

- **Direct HTTP only, no Twilio SDK** — avoids a new dependency; auth via HTTP Basic per Twilio docs
- **Inline TwiML for `!call`** (`Twiml` param, not `Url`) — no extra webhook endpoint needed for call bridging
- **California two-party consent** — `<Say voice="alice">This call may be recorded for quality.</Say>` is hard-coded before `<Dial>` and verified in tests
- **PhoneThreadStore** — reuses cc-connect's pattern (JSON file, atomic .tmp+mv writes); no new storage layer
- **Signature verification is mandatory** — `HandleInbound` rejects any request without a valid `X-Twilio-Signature`

## Testing

```bash
# Unit tests
go test ./platform/twilio/... -count=1 -v
go test ./platform/slack/commands/... -count=1 -v
go test ./platform/slack/... -run TestVistaHills -count=1 -v

# Integration tests (no credentials required)
go test ./tests/integration/twilio-bridge_test.go -count=1 -timeout 60s -v

# Full suite
go test ./... -count=1
```

## Deferred (requires real Twilio credentials)

Live smoke test procedure: [`docs/twilio-bridge-manual-smoke.md`](./twilio-bridge-manual-smoke.md)

Required env vars before live smoke:
- `TWILIO_ACCOUNT_SID`
- `TWILIO_AUTH_TOKEN`
- `TWILIO_FROM_NUMBER` (Vista Hills local number)
- `SAGAR_CELL_NUMBER`
- `SLACK_LEADS_CHANNEL` (default: `#chief-of-staff`)
- `CC_CONNECT_WEBHOOK_SECRET`
