package appsplatform

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"
)

// Outbound-event signing (Slack-compatible v0 scheme, identical to Talk's bot
// framework so receivers/verifiers port unchanged).
//
// For each outbound event POST the platform sends:
//
//	X-Vulos-Request-Timestamp: <unix seconds>
//	X-Vulos-Signature:         v0=<hex hmac-sha256>
//
// The signed string is exactly:
//
//	"<timestamp>" + "." + <raw request body bytes>
//
// signed with the app's signing secret using HMAC-SHA256. Receivers reproduce
// the signature over the timestamp + raw body they received and compare in
// constant time (see Verify). They SHOULD also reject stale timestamps to blunt
// replay (the example app uses a 5-minute window).

// SigHeaderTimestamp is the request header carrying the unix-seconds timestamp.
const SigHeaderTimestamp = "X-Vulos-Request-Timestamp"

// SigHeaderSignature is the request header carrying the "v0=" signature.
const SigHeaderSignature = "X-Vulos-Signature"

// sigBasestring builds the exact bytes that are HMAC'd: timestamp + "." + body.
func sigBasestring(timestamp string, body []byte) []byte {
	base := make([]byte, 0, len(timestamp)+1+len(body))
	base = append(base, timestamp...)
	base = append(base, '.')
	base = append(base, body...)
	return base
}

// Sign returns the "v0=<hex>" signature for (timestamp, body) under secret.
func Sign(timestamp string, body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(sigBasestring(timestamp, body))
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// Verify reports whether sig is a valid signature for (timestamp, body) under
// secret. The comparison is constant-time.
func Verify(timestamp string, body []byte, secret, sig string) bool {
	expected := Sign(timestamp, body, secret)
	return hmac.Equal([]byte(expected), []byte(sig))
}

// NowTimestamp returns the current unix-seconds timestamp as a string, suitable
// for the X-Vulos-Request-Timestamp header.
func NowTimestamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}
