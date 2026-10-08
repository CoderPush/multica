package lark

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/integrations/channel/engine"
	"github.com/multica-ai/multica/server/internal/service"
	dbfx "github.com/multica-ai/multica/server/internal/testutil"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type knowledgeAPIStub struct {
	messages     map[string]LarkMessage
	file         []byte
	member       bool
	fetchErr     error
	fetchWait    bool
	history      map[string]knowledgeHistoryPage
	historyCalls []knowledgeHistoryParams
	sends        []SendTextParams
	delivered    map[string]string
	loseReply    bool
	sendErr      error
}

func (f *knowledgeAPIStub) GetMessage(ctx context.Context, _ InstallationCredentials, id string) ([]LarkMessage, error) {
	if f.fetchWait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	m, ok := f.messages[id]
	if !ok {
		return nil, nil
	}
	return []LarkMessage{m}, nil
}
func (f *knowledgeAPIStub) DownloadMessageResourceStream(context.Context, InstallationCredentials, DownloadResourceParams) (DownloadedResourceStream, error) {
	return DownloadedResourceStream{Body: io.NopCloser(bytes.NewReader(f.file)), SizeBytes: int64(len(f.file))}, nil
}
func (f *knowledgeAPIStub) KnowledgeMember(context.Context, InstallationCredentials, ChatID, OpenID) (bool, error) {
	return f.member, nil
}
func (f *knowledgeAPIStub) KnowledgeHistory(_ context.Context, _ InstallationCredentials, p knowledgeHistoryParams) (knowledgeHistoryPage, error) {
	f.historyCalls = append(f.historyCalls, p)
	return f.history[p.ThreadID+":"+p.PageToken], nil
}
func (f *knowledgeAPIStub) SendTextMessage(_ context.Context, p SendTextParams) (string, error) {
	f.sends = append(f.sends, p)
	if f.sendErr != nil {
		return "", f.sendErr
	}
	if f.delivered == nil {
		f.delivered = map[string]string{}
	}
	id := f.delivered[p.UUID]
	if id == "" {
		id = "om_reply_" + fmt.Sprint(len(f.delivered))
		f.delivered[p.UUID] = id
	}
	if f.loseReply {
		f.loseReply = false
		return "", errors.New("connection lost after accepted reply")
	}
	return id, nil
}

type knowledgeStorageStub struct {
	objects      map[string][]byte
	beforeUpload func()
}

func (s *knowledgeStorageStub) ObjectURL(key string) string {
	return "https://private-storage.example/" + key
}
func (s *knowledgeStorageStub) Upload(_ context.Context, key string, data []byte, _, _ string) (string, error) {
	if s.beforeUpload != nil {
		s.beforeUpload()
	}
	if s.objects == nil {
		s.objects = map[string][]byte{}
	}
	s.objects[key] = data
	return s.ObjectURL(key), nil
}

func knowledgeFixture(t *testing.T) (*GroupKnowledge, *knowledgeAPIStub, *knowledgeModelStub, *dbfx.Fixture) {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for durable knowledge integration tests")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	fx := dbfx.New(pool, "", "")
	suffix := uuid.NewString()
	fx.UserID = fx.User(t, "Knowledge owner", "knowledge-"+suffix+"@example.test")
	fx.WorkspaceID = fx.Workspace(t, "Hiring fixture", "knowledge-"+suffix)
	fx.Member(t, fx.WorkspaceID, fx.UserID, "owner")
	agent := fx.Agent(t, "Private hiring agent", "")
	project := fx.Project(t, "CTO fixture")
	config, _ := json.Marshal(map[string]any{"app_id": "cli_fixture", "bot_open_id": "ou_bot", "region": "lark"})
	installation := fx.Insert(t, "channel_installation", dbfx.Cols{"workspace_id": fx.WorkspaceID, "agent_id": agent, "channel_type": "feishu", "config": config, "status": "active", "installer_user_id": fx.UserID})
	for name, value := range map[string]string{"Record type": "Candidate", "Review status": "Assessed", "Hiring stage": "New", "Assessment version": knowledgeJDVersion} {
		kind := "select"
		cfg := []byte(`{"options":[{"id":"value","name":"` + value + `","color":"blue"}]}`)
		if name == "Assessment version" {
			kind = "text"
			cfg = []byte(`{}`)
		}
		fx.Insert(t, "issue_property", dbfx.Cols{"workspace_id": fx.WorkspaceID, "name": name, "type": kind, "config": cfg})
	}
	fx.Cleanup(t, `DELETE FROM issue WHERE workspace_id=$1`, fx.WorkspaceID)
	fx.Cleanup(t, `DELETE FROM attachment WHERE workspace_id=$1`, fx.WorkspaceID)
	fx.Cleanup(t, `DELETE FROM lark_knowledge_state WHERE installation_id=$1`, installation)
	fx.Cleanup(t, `DELETE FROM lark_knowledge_file WHERE installation_id=$1`, installation)
	fx.Cleanup(t, `DELETE FROM lark_knowledge_assessment WHERE file_id IN (SELECT id FROM lark_knowledge_file WHERE installation_id=$1)`, installation)
	for _, table := range []string{"lark_knowledge_source", "lark_knowledge_job", "lark_knowledge_event"} {
		fx.Cleanup(t, "DELETE FROM "+table+" WHERE installation_id=$1", installation)
	}
	api := &knowledgeAPIStub{messages: map[string]LarkMessage{}, history: map[string]knowledgeHistoryPage{}, member: true, file: []byte("original test file")}
	assessment := knowledgeAssessment{IsCV: true, Name: "Example Person", Email: "example@professional.test", Summary: "Engineering leadership claims need verification."}
	for i := range knowledgeJDCriteria {
		assessment.Criteria = append(assessment.Criteria, knowledgeCriterion{Index: i, Level: "Not evidenced"})
	}
	raw, _ := json.Marshal(assessment)
	model := &knowledgeModelStub{output: string(raw)}
	q := db.New(pool)
	tasks := &service.TaskService{Queries: q, TxStarter: pool}
	g := &GroupKnowledge{policy: KnowledgePolicy{Mode: "process", InstallationID: installation, AppID: "cli_fixture", WorkspaceID: fx.WorkspaceID, AgentID: agent, ProjectID: project, OwnerID: fx.UserID, ChatID: "oc_fixture", StartTime: time.Now().Add(-time.Hour), Model: "fixture-only", DailyModelCalls: 20}, pool: pool, api: api, model: model, installations: NewChannelStore(q), credentials: fakeCredentials{secret: "fixture-only"}, storage: &knowledgeStorageStub{}, issues: &service.IssueService{Queries: q, TxStarter: pool, TaskService: tasks}, tasks: tasks, now: time.Now, logger: newDiscardLogger(), extract: func(context.Context, string, []byte) (string, error) {
		return "[page 1]\nExample Person\nexample@professional.test\nProfessional experience: AWS migration leadership.", nil
	}}
	return g, api, model, fx
}
func knowledgeUpload(g *GroupKnowledge, id string) InboundMessage {
	return InboundMessage{EventType: "im.message.receive_v1", EventID: "event:" + id, AppID: g.policy.AppID, ChatID: ChatID(g.policy.ChatID), ChatType: ChatTypeGroup, MessageID: id, SenderOpenID: "ou_user", SenderType: "user", MessageType: "file", Content: `{"file_key":"file_fixture","file_name":"resume.pdf"}`, CreateTime: fmt.Sprint(time.Now().UnixMilli())}
}
func knowledgeREST(g *GroupKnowledge, m InboundMessage) LarkMessage {
	return LarkMessage{ChatID: g.policy.ChatID, MessageID: m.MessageID, MessageType: m.MessageType, Content: m.Content, SenderID: string(m.SenderOpenID), SenderType: m.SenderType, CreateTime: m.CreateTime, ThreadID: m.ThreadID, MessageAppLink: "https://applink.larksuite.com/client/chat/open?openChatId=" + g.policy.ChatID + "&position=1"}
}
func knowledgeCapture(t *testing.T, g *GroupKnowledge, m InboundMessage) {
	t.Helper()
	inst := engine.ResolvedInstallation{ID: knowledgeUUID(g.policy.InstallationID), WorkspaceID: knowledgeUUID(g.policy.WorkspaceID), AgentID: knowledgeUUID(g.policy.AgentID), Active: true}
	handled, err := g.Capture(context.Background(), inst, channelMessageFromLark(m))
	if err != nil || !handled {
		t.Fatalf("capture: handled=%t err=%v", handled, err)
	}
}
func knowledgeDrain(t *testing.T, g *GroupKnowledge) {
	t.Helper()
	for i := 0; i < 40; i++ {
		j, err := g.nextJob(context.Background())
		if errors.Is(err, pgx.ErrNoRows) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = g.step(context.Background(), InstallationCredentials{}, j); err != nil {
			t.Fatalf("stage %s: %v", j.Stage, err)
		}
	}
	t.Fatal("jobs did not settle")
}
func knowledgeCount(t *testing.T, g *GroupKnowledge, query string, args ...any) int {
	t.Helper()
	var count int
	if err := g.pool.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestKnowledgeDurableIngestionRestartAndDigestDedupDB(t *testing.T) {
	g, api, model, _ := knowledgeFixture(t)
	m := knowledgeUpload(g, "om_upload")
	api.messages[m.MessageID] = knowledgeREST(g, m)
	// Concurrent event replay shares one source and one work item.
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- g.capture(context.Background(), m, false, true) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_job WHERE installation_id=$1`, g.policy.InstallationID); n != 1 {
		t.Fatalf("jobs=%d", n)
	}
	if model.calls != 0 {
		t.Fatal("ingress called a model")
	}
	j, err := g.nextJob(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = g.step(context.Background(), InstallationCredentials{}, j); err != nil {
		t.Fatal(err)
	}
	// A fresh worker only knows what survived the downloaded-stage commit.
	restarted := *g
	g = &restarted
	knowledgeDrain(t, g)
	if model.calls != 1 || len(api.delivered) != 1 {
		t.Fatalf("model calls=%d deliveries=%d", model.calls, len(api.delivered))
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM issue WHERE workspace_id=$1`, g.policy.WorkspaceID); n != 1 {
		t.Fatalf("candidates=%d", n)
	}
	m2 := knowledgeUpload(g, "om_reupload")
	api.messages[m2.MessageID] = knowledgeREST(g, m2)
	knowledgeCapture(t, g, m2)
	knowledgeDrain(t, g)
	if model.calls != 1 || len(api.delivered) != 1 {
		t.Fatalf("reupload repeated assessment/ACK: %d/%d", model.calls, len(api.delivered))
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM attachment WHERE workspace_id=$1`, g.policy.WorkspaceID); n != 1 {
		t.Fatalf("attachments=%d", n)
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_source WHERE installation_id=$1`, g.policy.InstallationID); n != 2 {
		t.Fatalf("provenance sources=%d", n)
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM agent_task_queue WHERE agent_id=$1`, g.policy.AgentID); n != 0 {
		t.Fatal("capture invoked private agent")
	}
}
func TestKnowledgeOutboxLostReplyAndExpiredWindowDB(t *testing.T) {
	g, api, _, _ := knowledgeFixture(t)
	m := knowledgeUpload(g, "om_upload")
	api.messages[m.MessageID] = knowledgeREST(g, m)
	knowledgeCapture(t, g, m)
	for i := 0; i < 6; i++ {
		j, err := g.nextJob(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if j.Kind == "ack" {
			break
		}
		if err = g.step(context.Background(), InstallationCredentials{}, j); err != nil {
			t.Fatal(err)
		}
	}
	j, err := g.nextJob(context.Background())
	if err != nil || j.Kind != "ack" {
		t.Fatal(j, err)
	}
	api.loseReply = true
	if err = g.step(context.Background(), InstallationCredentials{}, j); err == nil {
		t.Fatal("lost reply should be uncertain")
	}
	restarted := *g
	j, err = restarted.nextJob(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = restarted.step(context.Background(), InstallationCredentials{}, j); err != nil {
		t.Fatal(err)
	}
	if len(api.delivered) != 1 || len(api.sends) != 2 || api.sends[0].UUID != api.sends[1].UUID {
		t.Fatal("uncertain send was duplicated")
	}
	for _, p := range api.sends {
		if !p.ReplyTarget.InThread || p.ReplyTarget.MessageID != m.MessageID {
			t.Fatal("wrong reply transport")
		}
	}
	// Rewind only the test's outbox stage to simulate an unresolved old send.
	j.State.SentID = ""
	j.State.FirstSendAt = time.Now().Add(-time.Hour)
	if err = g.saveJob(context.Background(), j, "received"); err != nil {
		t.Fatal(err)
	}
	before := len(api.sends)
	if err = g.step(context.Background(), InstallationCredentials{}, j); err == nil {
		t.Fatal("expired send window was retried")
	}
	if len(api.sends) != before {
		t.Fatal("sent outside dedup window")
	}
}
func TestKnowledgeSourceReconciliationAndPrivateIsolationDB(t *testing.T) {
	g, api, model, fx := knowledgeFixture(t)
	fx.Issue(t, "Private candidate", dbfx.Cols{"description": "SECRET_PRIVATE_ASSESSMENT AWS concerns", "project_id": g.policy.ProjectID})
	source := knowledgeUpload(g, "om_evidence")
	source.MessageType = "text"
	source.Content = `{"text":"Owned an AWS migration."}`
	api.messages[source.MessageID] = knowledgeREST(g, source)
	knowledgeCapture(t, g, source)
	question := knowledgeUpload(g, "om_question")
	question.MessageType = "text"
	question.Content = `{"text":"AWS experience?"}`
	question.Body = "AWS experience?"
	question.CommandBody = question.Body
	question.AddressedToBot = true
	api.messages[question.MessageID] = knowledgeREST(g, question)
	knowledgeCapture(t, g, question)
	var sourceID string
	if err := g.pool.QueryRow(context.Background(), `SELECT id FROM lark_knowledge_source WHERE installation_id=$1 AND message_id=$2`, g.policy.InstallationID, source.MessageID).Scan(&sourceID); err != nil {
		t.Fatal(err)
	}
	model.output = `{"excerpts":[{"source_id":"` + sourceID + `","quote":"Owned an AWS migration."}]}`
	j, err := g.nextJob(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = g.step(context.Background(), InstallationCredentials{}, j); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(model.input, "SECRET_PRIVATE_ASSESSMENT") || strings.Contains(model.input, "Private candidate") {
		t.Fatal("private candidate entered group model input")
	}
	// Recall after answering but before delivery must invalidate the outbox.
	revoked := api.messages[source.MessageID]
	revoked.Deleted = true
	api.messages[source.MessageID] = revoked
	knowledgeDrain(t, g)
	if len(api.sends) != 0 {
		t.Fatal("recalled source was quoted")
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_source WHERE id=$1 AND available`, sourceID); n != 0 {
		t.Fatal("recalled source still searchable")
	}
	// A delayed receive replay must not revive the recalled snapshot.
	knowledgeCapture(t, g, source)
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_source WHERE id=$1 AND available`, sourceID); n != 0 {
		t.Fatal("old event revived source")
	}
}
func TestKnowledgeRejectsWrongScopeAndBotsDB(t *testing.T) {
	g, _, model, _ := knowledgeFixture(t)
	base := knowledgeUpload(g, "om_message")
	inst := engine.ResolvedInstallation{ID: knowledgeUUID(g.policy.InstallationID), WorkspaceID: knowledgeUUID(g.policy.WorkspaceID), AgentID: knowledgeUUID(g.policy.AgentID), Active: true}
	for _, tc := range []struct {
		name   string
		change func(*InboundMessage)
	}{{"other chat", func(m *InboundMessage) { m.ChatID = "oc_other" }}, {"DM", func(m *InboundMessage) { m.ChatType = ChatTypeP2P }}, {"app", func(m *InboundMessage) { m.AppID = "cli_other" }}, {"bot", func(m *InboundMessage) { m.SenderType = "app" }}} {
		t.Run(tc.name, func(t *testing.T) {
			m := base
			tc.change(&m)
			if _, err := g.Capture(context.Background(), inst, channelMessageFromLark(m)); err != nil {
				t.Fatal(err)
			}
		})
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_event WHERE installation_id=$1`, g.policy.InstallationID); n != 0 || model.calls != 0 {
		t.Fatal("wrong scope stored data or used model")
	}
}

func TestKnowledgeReconcilePaginationAndOutageDB(t *testing.T) {
	g, api, _, _ := knowledgeFixture(t)
	ctx := context.Background()
	old := knowledgeUpload(g, "om_old")
	old.MessageType, old.Content, old.ThreadID = "text", `{"text":"old version"}`, "omt_old"
	knowledgeCapture(t, g, old)
	edited := knowledgeREST(g, old)
	edited.Content = `{"text":"current version"}`
	api.messages[old.MessageID] = edited
	recent := knowledgeUpload(g, "om_recent")
	recent.MessageType, recent.Content = "text", `{"text":"new root"}`
	reply := knowledgeUpload(g, "om_reply")
	reply.MessageType, reply.Content, reply.ThreadID = "text", `{"text":"new reply"}`, old.ThreadID
	api.messages[recent.MessageID], api.messages[reply.MessageID] = knowledgeREST(g, recent), knowledgeREST(g, reply)
	api.history[":"] = knowledgeHistoryPage{Messages: []LarkMessage{api.messages[recent.MessageID]}, HasMore: true, Next: "page2"}
	api.history[":page2"] = knowledgeHistoryPage{}
	api.history[old.ThreadID+":"] = knowledgeHistoryPage{Messages: []LarkMessage{api.messages[reply.MessageID]}}
	_, err := g.pool.Exec(ctx, `INSERT INTO lark_knowledge_state(installation_id,workspace_id) VALUES($1,$2) ON CONFLICT(installation_id) DO NOTHING`, g.policy.InstallationID, g.policy.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.reconcile(ctx, Installation{}, InstallationCredentials{}); err != nil {
		t.Fatal(err)
	}
	if len(api.historyCalls) != 3 {
		t.Fatalf("history pages=%d", len(api.historyCalls))
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_source WHERE installation_id=$1 AND available`, g.policy.InstallationID); n != 3 {
		t.Fatalf("sources=%d", n)
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_source WHERE installation_id=$1 AND body='current version'`, g.policy.InstallationID); n != 1 {
		t.Fatal("edit not reconciled")
	}
	var watermark time.Time
	if err = g.pool.QueryRow(ctx, `SELECT history_through FROM lark_knowledge_state WHERE installation_id=$1`, g.policy.InstallationID).Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	api.fetchErr = errors.New("temporary outage")
	if err = g.reconcile(ctx, Installation{}, InstallationCredentials{}); err == nil {
		t.Fatal("outage must fail reconciliation")
	}
	var after time.Time
	if err = g.pool.QueryRow(ctx, `SELECT history_through FROM lark_knowledge_state WHERE installation_id=$1`, g.policy.InstallationID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if !after.Equal(watermark) {
		t.Fatal("failed reconciliation advanced watermark")
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_source WHERE installation_id=$1 AND available`, g.policy.InstallationID); n != 3 {
		t.Fatal("outage treated as deletion")
	}
	api.fetchErr = nil
	api.history[":"] = knowledgeHistoryPage{HasMore: true, Next: "loop"}
	api.history[":loop"] = knowledgeHistoryPage{HasMore: true, Next: "loop"}
	if err = g.reconcile(ctx, Installation{}, InstallationCredentials{}); err == nil {
		t.Fatal("pagination loop accepted")
	}
}

func TestKnowledgeSourceRestorationResumesOnlyFileDB(t *testing.T) {
	g, api, model, _ := knowledgeFixture(t)
	ctx := context.Background()
	m := knowledgeUpload(g, "om_restore")
	api.messages[m.MessageID] = knowledgeREST(g, m)
	knowledgeCapture(t, g, m)
	j, err := g.nextJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.invalidate(ctx, j.SourceID); err != nil {
		t.Fatal(err)
	}
	if err = g.step(ctx, InstallationCredentials{}, j); err != nil {
		t.Fatal(err)
	}
	if err = g.capture(ctx, m, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err = g.nextJob(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("event replay restored cancelled job")
	}
	if err = g.capture(ctx, g.fromREST(api.messages[m.MessageID]), true, false); err != nil {
		t.Fatal(err)
	}
	knowledgeDrain(t, g)
	if model.calls != 1 || len(api.delivered) != 1 {
		t.Fatal("restored upload did not finish")
	}
	if err = g.invalidate(ctx, j.SourceID); err != nil {
		t.Fatal(err)
	}
	if err = g.capture(ctx, g.fromREST(api.messages[m.MessageID]), true, false); err != nil {
		t.Fatal(err)
	}
	if _, err = g.nextJob(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal("completed upload restarted")
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_source WHERE id=$1 AND available AND extracted<>''`, j.SourceID); n != 1 {
		t.Fatal("restored file lost search text")
	}
}

func TestKnowledgeAmbiguousIdentityAndBudgetDB(t *testing.T) {
	g, api, model, fx := knowledgeFixture(t)
	ctx := context.Background()
	fx.Issue(t, "Example Person", dbfx.Cols{"project_id": g.policy.ProjectID, "description": "Human hiring notes"})
	m := knowledgeUpload(g, "om_ambiguous")
	api.messages[m.MessageID] = knowledgeREST(g, m)
	knowledgeCapture(t, g, m)
	for i := 0; i < 3; i++ {
		j, err := g.nextJob(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = g.step(ctx, InstallationCredentials{}, j); err != nil {
			t.Fatal(err)
		}
	}
	j, err := g.nextJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = g.step(ctx, InstallationCredentials{}, j)
	var terminal permanentKnowledgeError
	if !errors.As(err, &terminal) {
		t.Fatalf("name-only match accepted: %v", err)
	}
	if err = g.failJob(ctx, j, err); err != nil {
		t.Fatal(err)
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM issue WHERE workspace_id=$1 AND description='Human hiring notes'`, g.policy.WorkspaceID); n != 1 {
		t.Fatal("human record changed")
	}
	if len(api.sends) != 0 || model.calls != 1 {
		t.Fatal("ambiguous path sent reply or repeated assessment")
	}
	g.policy.DailyModelCalls = 1
	if err = g.reserveModelCall(ctx); !errors.As(err, &terminal) {
		t.Fatal("daily cap not enforced")
	}
	if _, err = g.pool.Exec(ctx, `UPDATE lark_knowledge_state SET model_day=CURRENT_DATE-1 WHERE installation_id=$1`, g.policy.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err = g.reserveModelCall(ctx); err != nil {
		t.Fatal("next day did not reset budget", err)
	}
}

func TestKnowledgeMemberDepartureCancelsOutboxDB(t *testing.T) {
	g, api, model, _ := knowledgeFixture(t)
	ctx := context.Background()
	m := knowledgeUpload(g, "om_question")
	m.MessageType, m.Content, m.CommandBody, m.AddressedToBot = "text", `{"text":"AWS?"}`, "AWS?", true
	api.messages[m.MessageID] = knowledgeREST(g, m)
	knowledgeCapture(t, g, m)
	j, err := g.nextJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.step(ctx, InstallationCredentials{}, j); err != nil {
		t.Fatal(err)
	}
	api.member = false
	knowledgeDrain(t, g)
	if len(api.sends) != 0 || model.calls != 0 {
		t.Fatal("departed member received answer")
	}
}

func TestKnowledgeWorkspaceTeardownRemovesPrivateEvidenceDB(t *testing.T) {
	g, api, _, _ := knowledgeFixture(t)
	ctx := context.Background()
	m := knowledgeUpload(g, "om_delete")
	api.messages[m.MessageID] = knowledgeREST(g, m)
	knowledgeCapture(t, g, m)
	knowledgeDrain(t, g)
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_assessment WHERE file_id IN (SELECT id FROM lark_knowledge_file WHERE installation_id=$1)`, g.policy.InstallationID); n != 1 {
		t.Fatal("fixture assessment missing")
	}
	var fileID string
	if err := g.pool.QueryRow(ctx, `SELECT id FROM lark_knowledge_file WHERE installation_id=$1`, g.policy.InstallationID).Scan(&fileID); err != nil {
		t.Fatal(err)
	}
	neighbor := uuid.NewString()
	if _, err := g.pool.Exec(ctx, `INSERT INTO lark_knowledge_state(installation_id,workspace_id) VALUES($1,$2)`, neighbor, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = g.pool.Exec(ctx, `DELETE FROM lark_knowledge_state WHERE installation_id=$1`, neighbor) })
	// Simulate hard installation removal before later workspace deletion.
	if _, err := g.pool.Exec(ctx, `DELETE FROM channel_installation WHERE id=$1`, g.policy.InstallationID); err != nil {
		t.Fatal(err)
	}
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err = db.New(tx).DeleteWorkspaceLeafData(ctx, knowledgeUUID(g.policy.WorkspaceID)); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"lark_knowledge_source", "lark_knowledge_event", "lark_knowledge_job", "lark_knowledge_file", "lark_knowledge_state"} {
		if n := knowledgeCount(t, g, `SELECT count(*) FROM `+table+` WHERE installation_id=$1`, g.policy.InstallationID); n != 0 {
			t.Fatalf("retained %s", table)
		}
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_assessment WHERE file_id=$1`, fileID); n != 0 {
		t.Fatal("orphan assessments retained")
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_state WHERE installation_id=$1`, neighbor); n != 1 {
		t.Fatal("neighbor state removed")
	}
}

func TestKnowledgeWorkerCaptureThenProcessAndRevokedOwnerDB(t *testing.T) {
	g, api, model, _ := knowledgeFixture(t)
	ctx := context.Background()
	m := knowledgeUpload(g, "om_tick")
	api.messages[m.MessageID] = knowledgeREST(g, m)
	g.policy.Mode = "capture"
	if err := g.capture(ctx, m, false, false); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := g.tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if model.calls != 0 || len(api.sends) != 0 {
		t.Fatal("capture mode produced side effects")
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM issue WHERE workspace_id=$1`, g.policy.WorkspaceID); n != 0 {
		t.Fatal("capture mode wrote candidate")
	}
	g.policy.Mode = "process"
	for i := 0; i < 10; i++ {
		if err := g.tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if model.calls != 1 || len(api.delivered) != 1 {
		t.Fatal("worker did not complete upload")
	}
	if _, err := g.pool.Exec(ctx, `DELETE FROM member WHERE workspace_id=$1 AND user_id=$2`, g.policy.WorkspaceID, g.policy.OwnerID); err != nil {
		t.Fatal(err)
	}
	if err := g.tick(ctx); err == nil {
		t.Fatal("revoked owner kept processing")
	}
}

func TestKnowledgeDefinitiveDeletionDoesNotStallReconciliationDB(t *testing.T) {
	for _, code := range []int{230110, 230011, 230050} {
		for _, httpStatus := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/http_status_%t", code, httpStatus), func(t *testing.T) {
				g, api, _, _ := knowledgeFixture(t)
				ctx := context.Background()
				m := knowledgeUpload(g, "om_gone")
				m.MessageType, m.Content = "text", `{"text":"obsolete source"}`
				knowledgeCapture(t, g, m)
				if _, err := g.pool.Exec(ctx, `INSERT INTO lark_knowledge_state(installation_id,workspace_id) VALUES($1,$2) ON CONFLICT(installation_id) DO NOTHING`, g.policy.InstallationID, g.policy.WorkspaceID); err != nil {
					t.Fatal(err)
				}
				api.fetchErr = &APIError{Op: "get message", Code: code, Msg: "gone"}
				if httpStatus {
					api.fetchErr = fmt.Errorf("get message: %w", &larkAPIStatusError{StatusCode: 400, Code: code, Msg: "gone"})
				}
				if err := g.reconcile(ctx, Installation{}, InstallationCredentials{}); err != nil {
					t.Fatal(err)
				}
				if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_source WHERE installation_id=$1 AND available`, g.policy.InstallationID); n != 0 {
					t.Fatal("deleted source remains available")
				}
				if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_state WHERE installation_id=$1 AND history_through IS NOT NULL AND last_error=''`, g.policy.InstallationID); n != 1 {
					t.Fatal("deleted source stalled watermark")
				}
			})
		}
	}

}

func TestKnowledgeOwnDeadlineBacksOffButShutdownPreservesStageDB(t *testing.T) {
	g, api, _, _ := knowledgeFixture(t)
	ctx := context.Background()
	g.stepTimeout = 100 * time.Millisecond
	m := knowledgeUpload(g, "om_timeout")
	knowledgeCapture(t, g, m)
	api.fetchWait = true
	if _, err := g.pool.Exec(ctx, `UPDATE lark_knowledge_state SET next_reconcile_at=now()+interval '1 hour' WHERE installation_id=$1`, g.policy.InstallationID); err != nil {
		t.Fatal(err)
	}
	if err := g.tick(ctx); err != nil {
		t.Fatal(err)
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_job WHERE installation_id=$1 AND attempts=1 AND available_at>now() AND stage='received'`, g.policy.InstallationID); n != 1 {
		t.Fatal("deadline did not persist retry/backoff")
	}
	if _, err := g.pool.Exec(ctx, `UPDATE lark_knowledge_job SET available_at=now() WHERE installation_id=$1`, g.policy.InstallationID); err != nil {
		t.Fatal(err)
	}
	g.stepTimeout = time.Second
	parent, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if err := g.tick(parent); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("parent cancellation was swallowed", err)
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_job WHERE installation_id=$1 AND attempts=1 AND stage='received'`, g.policy.InstallationID); n != 1 {
		t.Fatal("shutdown consumed retry budget")
	}
}

func TestKnowledgeSharedContactEmailNeverMergesDifferentPeopleDB(t *testing.T) {
	g, api, _, fx := knowledgeFixture(t)
	ctx := context.Background()
	metadata, _ := json.Marshal(map[string]string{"tarley_email_sha256": knowledgeEmailDigest("example@professional.test")})
	id := fx.Issue(t, "Different Person", dbfx.Cols{"project_id": g.policy.ProjectID, "description": "Human interview notes", "metadata": metadata})
	m := knowledgeUpload(g, "om_shared_contact")
	api.messages[m.MessageID] = knowledgeREST(g, m)
	knowledgeCapture(t, g, m)
	for i := 0; i < 3; i++ {
		j, err := g.nextJob(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = g.step(ctx, InstallationCredentials{}, j); err != nil {
			t.Fatal(err)
		}
	}
	j, err := g.nextJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = g.step(ctx, InstallationCredentials{}, j)
	var terminal permanentKnowledgeError
	if !errors.As(err, &terminal) {
		t.Fatal("different candidate merged through shared email", err)
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM issue WHERE id=$1 AND title='Different Person' AND description='Human interview notes'`, id); n != 1 {
		t.Fatal("existing candidate overwritten")
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM attachment WHERE workspace_id=$1`, g.policy.WorkspaceID); n != 0 {
		t.Fatal("new CV attached to wrong candidate")
	}
	// Cached assessment links are also rechecked after a human renames a record.
	if _, err = g.pool.Exec(ctx, `UPDATE lark_knowledge_assessment SET issue_id=$2 WHERE file_id=$1`, j.State.FileID, id); err != nil {
		t.Fatal(err)
	}
	if err = g.step(ctx, InstallationCredentials{}, j); !errors.As(err, &terminal) {
		t.Fatal("cached link bypassed identity conflict", err)
	}
}

func TestKnowledgeCandidateRenameDuringUploadDB(t *testing.T) {
	g, api, _, fx := knowledgeFixture(t)
	ctx := context.Background()
	metadata, _ := json.Marshal(map[string]string{"tarley_email_sha256": knowledgeEmailDigest("example@professional.test")})
	id := fx.Issue(t, "Example Person", dbfx.Cols{"project_id": g.policy.ProjectID, "description": "Human interview notes", "metadata": metadata})
	m := knowledgeUpload(g, "om_rename_during_upload")
	api.messages[m.MessageID] = knowledgeREST(g, m)
	knowledgeCapture(t, g, m)
	for i := 0; i < 3; i++ {
		j, err := g.nextJob(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = g.step(ctx, InstallationCredentials{}, j); err != nil {
			t.Fatal(err)
		}
	}
	g.storage.(*knowledgeStorageStub).beforeUpload = func() {
		// Commit a human identity correction after matching but before locking.
		if _, err := g.pool.Exec(ctx, `UPDATE issue SET title='Different Person' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	j, err := g.nextJob(ctx)
	if err != nil {
		t.Fatal(err)
	}
	err = g.step(ctx, InstallationCredentials{}, j)
	var terminal permanentKnowledgeError
	if !errors.As(err, &terminal) {
		t.Fatal("identity correction during upload was ignored", err)
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM issue WHERE id=$1 AND title='Different Person' AND description='Human interview notes' AND metadata=$2::jsonb`, id, metadata); n != 1 {
		t.Fatal("candidate assessment or metadata changed")
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM attachment WHERE workspace_id=$1`, g.policy.WorkspaceID); n != 0 {
		t.Fatal("new CV attached after identity changed")
	}
	if n := knowledgeCount(t, g, `SELECT count(*) FROM lark_knowledge_assessment WHERE file_id=$1 AND issue_id IS NOT NULL`, j.State.FileID); n != 0 {
		t.Fatal("assessment bound to a different person")
	}
}
