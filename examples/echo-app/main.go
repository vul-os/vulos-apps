// Command echo-app is a tiny, dependency-free example of a Vulos app/bot built
// on the shared Apps & Bots platform. It is product-agnostic: it receives signed
// outbound events from ANY Vulos product (Talk/Mail/Meet/Office) at /events,
// verifies the X-Vulos-Signature over "<timestamp>.<rawBody>" using its signing
// secret, and — on app_mention or slash_command events — calls back the product
// runtime API (POST {base}/v1/act) with its token to echo the text into the
// originating target (channel / thread / room / doc).
//
// It uses only the Go standard library so it compiles as part of `go build ./...`
// with no external module dependencies.
//
// Usage:
//
//	VULOS_BASE_URL=http://localhost:8080 \
//	VULOS_APPS_PATH=/api/apps \
//	VULOS_APP_TOKEN=vat_... \
//	VULOS_APP_SIGNING_SECRET=vas_... \
//	go run ./examples/echo-app
//
// Then point the app's webhook_url at http://<this-host>:8091/events.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

// event mirrors the platform outbound event envelope (appsplatform.Event).
type event struct {
	Type      string         `json:"type"`
	AppID     string         `json:"app_id"`
	Product   string         `json:"product"`
	Event     map[string]any `json:"event"`
	EventTime int64          `json:"event_time"`
}

const (
	sigHeaderTimestamp = "X-Vulos-Request-Timestamp"
	sigHeaderSignature = "X-Vulos-Signature"
	maxSkewSeconds     = 300 // reject events older than 5 minutes (replay defense)
)

func main() {
	base := env("VULOS_BASE_URL", "http://localhost:8080")
	appsPath := env("VULOS_APPS_PATH", "/api/apps")
	token := os.Getenv("VULOS_APP_TOKEN")
	secret := os.Getenv("VULOS_APP_SIGNING_SECRET")
	addr := env("ECHO_APP_ADDR", ":8091")
	if token == "" || secret == "" {
		log.Fatal("set VULOS_APP_TOKEN and VULOS_APP_SIGNING_SECRET")
	}

	a := &echoApp{base: base, appsPath: appsPath, token: token, secret: secret, client: &http.Client{Timeout: 5 * time.Second}}
	http.HandleFunc("/events", a.handleEvents)
	log.Printf("echo-app listening on %s, talking to %s%s", addr, base, appsPath)
	log.Fatal(http.ListenAndServe(addr, nil))
}

type echoApp struct {
	base     string
	appsPath string
	token    string
	secret   string
	client   *http.Client
}

func (a *echoApp) handleEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}
	ts := r.Header.Get(sigHeaderTimestamp)
	sig := r.Header.Get(sigHeaderSignature)
	if !verify(ts, body, a.secret, sig) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	// Always 200 quickly; do the echo work without blocking the ack.
	w.WriteHeader(http.StatusOK)

	var ev event
	if err := json.Unmarshal(body, &ev); err != nil {
		return
	}
	switch ev.Type {
	case "app_mention", "slash_command":
		target, _ := ev.Event["target"].(string)
		if target == "" {
			target, _ = ev.Event["channel_id"].(string) // tolerate Talk-style payloads
		}
		text, _ := ev.Event["text"].(string)
		if target == "" {
			return
		}
		go a.echo(target, "echo: "+text)
	}
}

// echo posts a generic action back to the product's runtime API. The product's
// adapter interprets the action+payload for its own surface; "message.post" with
// {text} is the Talk convention.
func (a *echoApp) echo(target, text string) {
	payload, _ := json.Marshal(map[string]string{"text": text})
	body, _ := json.Marshal(map[string]any{
		"action":  "message.post",
		"target":  target,
		"payload": json.RawMessage(payload),
	})
	req, err := http.NewRequest(http.MethodPost, a.base+a.appsPath+"/v1/act", bytes.NewReader(body))
	if err != nil {
		log.Printf("build request: %v", err)
		return
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		log.Printf("act: %v", err)
		return
	}
	resp.Body.Close()
}

// verify recomputes the v0 signature over "<ts>.<body>" and compares constant
// time, rejecting stale timestamps to blunt replay.
func verify(ts string, body []byte, secret, sig string) bool {
	n, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if d := time.Now().Unix() - n; d > maxSkewSeconds || d < -maxSkewSeconds {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	expected := "v0=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
