package lark

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	"github.com/multica-ai/multica/server/internal/util"
)

func TestIngressRelayReceiptAndLiteralBoundary(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	secret := strings.Repeat("s", 32)
	for _, tc := range []struct {
		name, receipt, disposition string
		status                     int
		wantErr, consume           bool
	}{
		{"consume", `{"version":1,"event_id":"evt","durable":true,"disposition":"consume"}`, "consume", 200, false, true},
		{"observe", `{"version":1,"event_id":"evt","durable":true,"disposition":"observe"}`, "observe", 200, false, false},
		{"mode drift", `{"version":1,"event_id":"evt","durable":true,"disposition":"observe"}`, "consume", 200, true, false},
		{"wrong event", `{"version":1,"event_id":"other","durable":true,"disposition":"consume"}`, "consume", 200, true, false},
		{"not committed", `{"version":1,"event_id":"evt","durable":false,"disposition":"consume"}`, "consume", 200, true, false},
		{"invalid", "{}", "consume", 200, true, false},
		{"unavailable", "{}", "consume", 503, true, false},
		{"redirect", "{}", "consume", 302, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				b, _ := io.ReadAll(r.Body)
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write([]byte(r.Header.Get("X-Multica-Timestamp") + "."))
				mac.Write(b)
				if hex.EncodeToString(mac.Sum(nil)) != r.Header.Get("X-Multica-Signature") {
					t.Error("signature mismatch")
				}
				if strings.Contains(string(b), "PRIVATE") {
					t.Error("enriched context leaked")
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.receipt))
			}))
			defer server.Close()
			raw, _ := json.Marshal(ingressRelayConfig{InstallationID: id, AppID: "cli_test", ChatID: "oc_test", Endpoint: server.URL, Secret: secret, Disposition: tc.disposition})
			relay, err := NewIngressRelay(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			uid, _ := util.ParseUUID(id)
			inst := engine.ResolvedInstallation{ID: uid, Active: true, Platform: Installation{AppID: "cli_test", AppSecretEncrypted: []byte("PRIVATE")}}
			lm := InboundMessage{EventID: "evt", AppID: "cli_test", ChatID: "oc_test", ChatType: ChatTypeGroup, Body: "PRIVATE", Content: `{"text":"hello"}`}
			b, _ := json.Marshal(lm)
			m := channel.InboundMessage{Raw: b, Source: channel.Source{ChatType: channel.ChatTypeGroup}}
			for i := 0; i < 2; i++ {
				got, err := relay.Capture(context.Background(), inst, m)
				if (err != nil) != tc.wantErr || got != tc.consume {
					t.Fatalf("got %v %v", got, err)
				}
			}
			if calls != 2 {
				t.Fatal("duplicates must be durably handled by receiver")
			}
			m.Source.ChatType = channel.ChatTypeP2P
			got, err := relay.Capture(context.Background(), inst, m)
			if got || err != nil || calls != 2 {
				t.Fatal("DM forwarded")
			}
		})
	}
}
func TestIngressRelayConfig(t *testing.T) {
	if r, e := NewIngressRelay(""); r != nil || e != nil {
		t.Fatal("disabled relay")
	}
	for _, s := range []string{`{}`, `{"unknown":true}`, `{} {}`} {
		if _, e := NewIngressRelay(s); e == nil {
			t.Fatal("invalid accepted")
		}
	}
}
