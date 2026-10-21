package lark

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	"github.com/multica-ai/multica/server/internal/util"
)

// IngressRelay forwards one explicitly configured group to a durable consumer.
// It adds no queue: an unavailable receiver fails the connector ACK, allowing
// provider retries. Receivers must reconcile missed events independently.
type IngressRelay struct {
	config ingressRelayConfig
	client *http.Client
}
type ingressRelayConfig struct {
	InstallationID string `json:"installation_id"`
	AppID          string `json:"app_id"`
	ChatID         string `json:"chat_id"`
	Endpoint       string `json:"endpoint"`
	Secret         string `json:"secret"`
	Disposition    string `json:"disposition"`
}

func NewIngressRelay(raw string) (*IngressRelay, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var c ingressRelayConfig
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if dec.Decode(&c) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid ingress relay JSON")
	}
	u, err := url.Parse(c.Endpoint)
	_, idErr := util.ParseUUID(c.InstallationID)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"))) || idErr != nil || !strings.HasPrefix(c.AppID, "cli_") || !strings.HasPrefix(c.ChatID, "oc_") || len(c.Secret) < 32 || (c.Disposition != "consume" && c.Disposition != "observe") {
		return nil, errors.New("invalid ingress relay target, secret or disposition")
	}
	return &IngressRelay{config: c, client: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// The v1 envelope deliberately contains literal source content only. Enriched
// quotes/history, credentials and private session context never cross this seam.
type ingressEnvelope struct {
	Version        int    `json:"version"`
	InstallationID string `json:"installation_id"`
	WorkspaceID    string `json:"workspace_id"`
	AgentID        string `json:"agent_id"`
	AppID          string `json:"app_id"`
	EventID        string `json:"event_id"`
	EventType      string `json:"event_type"`
	ChatID         string `json:"chat_id"`
	ChatType       string `json:"chat_type"`
	MessageID      string `json:"message_id"`
	SenderID       string `json:"sender_id"`
	SenderType     string `json:"sender_type"`
	MessageType    string `json:"message_type"`
	Content        string `json:"content"`
	CreateTime     string `json:"create_time"`
	ParentID       string `json:"parent_id"`
	RootID         string `json:"root_id"`
	ThreadID       string `json:"thread_id"`
	AddressedToBot bool   `json:"addressed_to_bot"`
	CommandBody    string `json:"command_body"`
}

func (r *IngressRelay) Capture(ctx context.Context, inst engine.ResolvedInstallation, msg channel.InboundMessage) (bool, error) {
	if !inst.Active || uuidString(inst.ID) != r.config.InstallationID || msg.Source.ChatType != channel.ChatTypeGroup {
		return false, nil
	}
	m, err := larkMsgFromRaw(msg)
	if err != nil {
		return false, errors.New("ingress source unavailable")
	}
	if string(m.ChatID) != r.config.ChatID || m.AppID != r.config.AppID {
		return false, nil
	}
	// Use the server-resolved installation rather than accepting an app identity
	// solely from the event payload. Never serialize Platform (it holds secrets).
	native, ok := inst.Platform.(Installation)
	if !ok || native.AppID != r.config.AppID {
		return false, errors.New("ingress installation mismatch")
	}
	if m.EventID == "" {
		return false, errors.New("ingress event ID missing")
	}
	body, err := json.Marshal(ingressEnvelope{Version: 1, InstallationID: uuidString(inst.ID), WorkspaceID: uuidString(inst.WorkspaceID), AgentID: uuidString(inst.AgentID), AppID: m.AppID, EventID: m.EventID, EventType: m.EventType, ChatID: string(m.ChatID), ChatType: string(m.ChatType), MessageID: m.MessageID, SenderID: string(m.SenderOpenID), SenderType: m.SenderType, MessageType: m.MessageType, Content: m.Content, CreateTime: m.CreateTime, ParentID: m.ParentID, RootID: m.RootID, ThreadID: m.ThreadID, AddressedToBot: m.AddressedToBot, CommandBody: m.CommandBody})
	if err != nil || len(body) > 512<<10 {
		return false, errors.New("ingress source exceeds limit")
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(r.config.Secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.Endpoint, bytes.NewReader(body))
	if err != nil {
		return false, errors.New("ingress request invalid")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Timestamp", timestamp)
	req.Header.Set("X-Multica-Signature", hex.EncodeToString(mac.Sum(nil)))
	resp, err := r.client.Do(req)
	if err != nil {
		return false, errors.New("ingress receiver unavailable")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, errors.New("ingress receiver rejected event")
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if err != nil || len(b) > 4096 {
		return false, errors.New("ingress receipt unavailable")
	}
	var receipt struct {
		Version     int    `json:"version"`
		EventID     string `json:"event_id"`
		Durable     bool   `json:"durable"`
		Disposition string `json:"disposition"`
	}
	if json.Unmarshal(b, &receipt) != nil || receipt.Version != 1 || receipt.EventID != m.EventID || !receipt.Durable || receipt.Disposition != r.config.Disposition {
		return false, errors.New("ingress receipt mismatch")
	}
	return r.config.Disposition == "consume", nil
}
