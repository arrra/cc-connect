package twilio

// MessageStatus represents the delivery status of a Twilio SMS message.
type MessageStatus string

const (
	MessageStatusQueued      MessageStatus = "queued"
	MessageStatusSending     MessageStatus = "sending"
	MessageStatusSent        MessageStatus = "sent"
	MessageStatusDelivered   MessageStatus = "delivered"
	MessageStatusUndelivered MessageStatus = "undelivered"
	MessageStatusFailed      MessageStatus = "failed"
	MessageStatusReceived    MessageStatus = "received"
)

// CallStatus represents the status of a Twilio voice call.
type CallStatus string

const (
	CallStatusQueued     CallStatus = "queued"
	CallStatusRinging    CallStatus = "ringing"
	CallStatusInProgress CallStatus = "in-progress"
	CallStatusCompleted  CallStatus = "completed"
	CallStatusBusy       CallStatus = "busy"
	CallStatusNoAnswer   CallStatus = "no-answer"
	CallStatusCanceled   CallStatus = "canceled"
	CallStatusFailed     CallStatus = "failed"
)

// Message is the Twilio Message resource returned by the Messages API.
type Message struct {
	SID          string        `json:"sid"`
	AccountSID   string        `json:"account_sid"`
	From         string        `json:"from"`
	To           string        `json:"to"`
	Body         string        `json:"body"`
	Status       MessageStatus `json:"status"`
	DateCreated  string        `json:"date_created"`
	ErrorCode    *int          `json:"error_code"`
	ErrorMessage string        `json:"error_message"`
}

// Call is the Twilio Call resource returned by the Calls API.
type Call struct {
	SID         string     `json:"sid"`
	AccountSID  string     `json:"account_sid"`
	From        string     `json:"from"`
	To          string     `json:"to"`
	Status      CallStatus `json:"status"`
	DateCreated string     `json:"date_created"`
}

// Inbound represents a parsed inbound SMS from Twilio's webhook.
type Inbound struct {
	MessageSID string
	AccountSID string
	From       string
	Body       string
}

// twilioError is returned by the Twilio API for 4xx/5xx responses.
type twilioError struct {
	Code     int    `json:"code"`
	Message  string `json:"message"`
	MoreInfo string `json:"more_info"`
	Status   int    `json:"status"`
}

func (e *twilioError) Error() string {
	return e.Message
}
