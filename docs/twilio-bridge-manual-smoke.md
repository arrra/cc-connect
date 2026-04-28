# Twilio Bridge Manual Smoke Test

Run these steps after deploying with real Twilio credentials set.

**Required env vars before testing:**
```
TWILIO_ACCOUNT_SID=ACxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
TWILIO_AUTH_TOKEN=<your-auth-token>
TWILIO_FROM_NUMBER=+1916XXXXXXX
TWILIO_INBOUND_WEBHOOK_SECRET=<shared-secret>
SAGAR_CELL_NUMBER=+1XXXXXXXXXX
SLACK_LEADS_CHANNEL=#chief-of-staff
```

---

## 1. Inbound SMS → Slack thread

Simulate a Twilio inbound webhook against the deployed cc-connect endpoint.

```bash
# Generate a valid Twilio signature for the test payload.
# Replace HOST and SECRET with your deployment values.
HOST=https://your-cc-connect.example.com
SECRET=$TWILIO_AUTH_TOKEN
URL="${HOST}/twilio/inbound-sms"

# Compute signature: base64(HMAC-SHA1(authToken, url + sorted_params))
# Use the twilio-signature helper or the script below.
PARAMS="AccountSid=AC123&Body=Hello+from+test&From=%2B19165550123&MessageSid=SM00000000test"
SIG=$(python3 -c "
import hmac, hashlib, base64, urllib.parse
token='$SECRET'
url='$URL'
params=urllib.parse.parse_qs('$PARAMS', keep_blank_values=True)
sorted_keys = sorted(params.keys())
msg = url + ''.join(k + params[k][0] for k in sorted_keys)
sig = base64.b64encode(hmac.new(token.encode(), msg.encode(), hashlib.sha1).digest()).decode()
print(sig)
")

curl -s -X POST "$URL" \
  -H "X-Twilio-Signature: $SIG" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  --data-urlencode "From=+19165550123" \
  --data-urlencode "Body=Hello from test" \
  --data-urlencode "MessageSid=SM00000000test" \
  --data-urlencode "AccountSid=AC123"
```

**Expected:** A new thread appears in `#chief-of-staff` with the message body.

---

## 2. !sms — outbound SMS from Slack

1. Open the Slack lead thread created in step 1 (or any existing lead thread).
2. Type `!sms This is a test from cc-connect` in the thread.
3. **Expected:**
   - A confirmation message appears in the thread: `✓ SMS sent via (XXX) XXX-XXXX [sid: SMxxxxxx…]`
   - The Twilio Console → Messaging → Logs shows an outbound message to the lead's number.
   - Body matches what you typed.

**Test failure cases to verify manually:**
- Type `!sms` (no text) → "message cannot be empty" reply.
- Type `!sms` in a non-lead channel (main channel) → usage help reply.

---

## 3. !call — click-to-call from Slack

> **Note:** This requires `SAGAR_CELL_NUMBER` to be set to Sagar's cell and a TwiML endpoint configured.

1. In a lead thread, type `!call`.
2. **Expected:**
   - Slack thread reply: `📞 Calling <lead name> at (XXX) XXX-XXXX from (XXX) XXX-XXXX — your phone will ring shortly.`
   - Sagar's cell rings.
   - After answering, the TwiML preamble plays: *"This call may be recorded for quality."*
   - Lead's phone rings; bridge connects.
   - Twilio Console → Voice → Logs shows the call with recording enabled.

---

## 4. Lead state surfaced in Slack threads

> Requires the Vista Hills Worker to be deployed and configured to call cc-connect.

1. Trigger a new lead creation via the Worker (or POST directly):

```bash
curl -s -X POST https://your-cc-connect.example.com/vista-hills/lead-created \
  -H "Content-Type: application/json" \
  -H "X-CC-Connect-Secret: $CC_CONNECT_WEBHOOK_SECRET" \
  -d '{
    "lead_id": 9999,
    "phone": "+19165559999",
    "name": "Test Lead",
    "source": "meta",
    "campaign": "Smoke-Test",
    "form_submitted_at": "2026-04-28T14:00:00Z"
  }'
```

2. **Expected:** A new thread appears in `#chief-of-staff` with the formatted lead block.
3. POST a state-update:

```bash
curl -s -X POST https://your-cc-connect.example.com/vista-hills/lead-state-update \
  -H "Content-Type: application/json" \
  -H "X-CC-Connect-Secret: $CC_CONNECT_WEBHOOK_SECRET" \
  -d '{
    "lead_id": 9999,
    "from_state": "new",
    "to_state": "qualified",
    "qualifying_data": {"care_for": "mother", "area": "Folsom"}
  }'
```

4. **Expected:** A thread reply in the lead thread with qualifying summary.

---

## Notes

- Real Twilio calls cost money. Use Twilio's test credentials for unit/integration testing.
- The signature verification (`X-Twilio-Signature`) is mandatory and cannot be bypassed. Double-check the `HOST` URL matches exactly what Twilio sends (including `https://` and no trailing slash).
- Two-party consent preamble ("This call may be recorded for quality.") is hard-coded in TwiML. Do NOT remove it — California law.
