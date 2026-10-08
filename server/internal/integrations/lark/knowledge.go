package lark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/integrations/channel"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// KnowledgePolicy enables exactly one installation/group. Configuration alone
// never grants Lark permissions or private agent invocation. An empty setting is
// disabled; an incomplete setting is an error, not a partially active feature.
type KnowledgePolicy struct {
	Mode            string    `json:"mode"`
	InstallationID  string    `json:"installation_id"`
	AppID           string    `json:"app_id"`
	WorkspaceID     string    `json:"workspace_id"`
	AgentID         string    `json:"agent_id"`
	ProjectID       string    `json:"project_id"`
	OwnerID         string    `json:"owner_id"`
	ChatID          string    `json:"chat_id"`
	StartTime       time.Time `json:"start_time"`
	Model           string    `json:"model"`
	DailyModelCalls int       `json:"daily_model_calls"`
}

func ParseKnowledgePolicy(raw string) (*KnowledgePolicy, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var p KnowledgePolicy
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, errors.New("invalid MULTICA_LARK_KNOWLEDGE_POLICY JSON")
	}
	if dec.Decode(new(any)) != io.EOF {
		return nil, errors.New("invalid trailing knowledge policy JSON")
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

func (p KnowledgePolicy) validate() error {
	for _, id := range []string{p.InstallationID, p.WorkspaceID, p.AgentID, p.ProjectID, p.OwnerID} {
		if _, err := util.ParseUUID(id); err != nil {
			return errors.New("knowledge policy requires valid installation, workspace, agent, project and owner UUIDs")
		}
	}
	if (p.Mode != "capture" && p.Mode != "process") || !strings.HasPrefix(p.ChatID, "oc_") || !strings.HasPrefix(p.AppID, "cli_") || p.StartTime.IsZero() || p.Model == "" || p.DailyModelCalls < 1 || p.DailyModelCalls > 100 {
		return errors.New("knowledge policy requires capture/process mode, one chat/app, start_time, explicit model and daily_model_calls (1..100)")
	}
	return nil
}

func (p KnowledgePolicy) matches(inst engine.ResolvedInstallation, m InboundMessage) bool {
	return inst.Active && uuidString(inst.ID) == p.InstallationID && uuidString(inst.WorkspaceID) == p.WorkspaceID && uuidString(inst.AgentID) == p.AgentID && m.AppID == p.AppID && string(m.ChatID) == p.ChatID && m.ChatType == ChatTypeGroup
}

type knowledgeModel interface {
	Enabled() bool
	GenerateJSON(context.Context, string, string, string, float64, int64) (string, error)
}

// GroupKnowledge uses the existing Lark connection and the server's configured
// storage/model services. It never creates a chat session or invokes an agent.
// The question path is handed only knowledgeEvidence, never candidate rows.
type GroupKnowledge struct {
	policy        KnowledgePolicy
	pool          *pgxpool.Pool
	api           knowledgeAPI
	installations *ChannelStore
	credentials   CredentialsResolver
	model         knowledgeModel
	storage       mediaStorage
	issues        *service.IssueService
	tasks         *service.TaskService
	extract       func(context.Context, string, []byte) (string, error)
	now           func() time.Time
	stepTimeout   time.Duration
	logger        *slog.Logger
}

func NewGroupKnowledge(p KnowledgePolicy, pool *pgxpool.Pool, api APIClient, creds CredentialsResolver, model knowledgeModel, storage mediaStorage, issues *service.IssueService, tasks *service.TaskService) (*GroupKnowledge, error) {
	if err := p.validate(); err != nil {
		return nil, err
	}
	kapi, ok := api.(knowledgeAPI)
	if !ok || pool == nil || creds == nil || issues == nil || tasks == nil || (p.Mode == "process" && (model == nil || !model.Enabled() || storage == nil)) {
		return nil, errors.New("knowledge requires database, Lark API, credentials, configured assist model, storage and private issue services")
	}
	return &GroupKnowledge{policy: p, pool: pool, api: kapi, installations: NewChannelStore(db.New(pool)), credentials: creds, model: model, storage: storage, issues: issues, tasks: tasks, extract: extractKnowledgeFile, now: time.Now, logger: slog.Default()}, nil
}

// Capture runs after the router validated the installation and before its
// mention/identity filters. All messages in the opted-in group are consumed:
// a group member's mention cannot reach the private Tarley agent by falling
// through after an ingestion or query error. Private DMs retain the old route.
func (g *GroupKnowledge) Capture(ctx context.Context, inst engine.ResolvedInstallation, msg channel.InboundMessage) (bool, error) {
	lm, err := larkMsgFromRaw(msg)
	if err != nil {
		return false, err
	}
	if !g.policy.matches(inst, lm) {
		return false, nil
	}
	if lm.SenderType != "user" || lm.SenderOpenID == "" || lm.MessageID == "" || lm.EventID == "" || lm.EventType != "im.message.receive_v1" {
		return true, nil
	}
	// Body can contain enriched quotes/recent history. Only the literal source
	// payload is group evidence. Never follow document URLs or forwarded items.
	lm.Body = flattenContent(lm.MessageType, lm.Content)
	lm.HasSelectedContext = false
	if len(lm.Content) > 512*1024 {
		return true, errors.New("knowledge event exceeds source limit")
	}
	err = g.capture(ctx, lm, false, g.policy.Mode == "process")
	return g.policy.Mode == "process", err
}

func knowledgeRevision(m InboundMessage, deleted bool) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%t", m.MessageType, m.Content, deleted)))
	return hex.EncodeToString(sum[:])
}

// knowledgeUUID is for policy IDs validated at construction and UUID columns
// read back from PostgreSQL. Invalid trusted IDs indicate a programming error.
func knowledgeUUID(s string) pgtype.UUID {
	id, err := util.ParseUUID(s)
	if err != nil {
		panic("lark knowledge: invalid trusted UUID")
	}
	return id
}

type knowledgeEvidence struct {
	ID        string         `json:"id"`
	Revision  string         `json:"revision"`
	Text      string         `json:"text"`
	Message   InboundMessage `json:"-"`
	CheckedAt time.Time      `json:"checked_at"`
}

// permanentKnowledgeError represents a safe, actionable terminal condition.
// It remains in the private jobs table for an operator to reconcile/retry.
type permanentKnowledgeError string

func (e permanentKnowledgeError) Error() string { return string(e) }
