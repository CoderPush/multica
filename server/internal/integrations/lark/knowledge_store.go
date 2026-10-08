package lark

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// capture stores event provenance, the current source and jobs in one commit.
// A replayed receive event cannot roll back a newer REST-verified edit/recall.
func (g *GroupKnowledge) capture(ctx context.Context, m InboundMessage, authoritative, live bool) error {
	return g.captureSource(ctx, m, authoritative, live, false)
}
func (g *GroupKnowledge) captureSource(ctx context.Context, m InboundMessage, authoritative, live, deleted bool) error {
	if m.ChatID != ChatID(g.policy.ChatID) || m.SenderType != "user" || m.MessageID == "" || m.AppID != g.policy.AppID {
		return nil
	}
	m.Body = flattenContent(m.MessageType, m.Content)
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	revision := knowledgeRevision(m, deleted)
	eventKey := m.EventID
	if authoritative {
		eventKey = "snapshot:" + m.MessageID + ":" + revision
	}
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Workspace teardown takes FOR UPDATE. Fence FK-free source writes so
	// a delayed ingress cannot recreate retained data after workspace deletion.
	var workspace string
	if err = tx.QueryRow(ctx, `SELECT id FROM workspace WHERE id=$1 FOR KEY SHARE`, g.policy.WorkspaceID).Scan(&workspace); err != nil {
		return err
	}
	// Keep workspace ownership even if the installation is later reclaimed.
	// Workspace teardown must still be able to remove every retained original.
	_, err = tx.Exec(ctx, `INSERT INTO lark_knowledge_state(installation_id,workspace_id) VALUES($1,$2) ON CONFLICT(installation_id) DO NOTHING`, g.policy.InstallationID, g.policy.WorkspaceID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO lark_knowledge_event(installation_id,event_key,payload) VALUES($1,$2,$3) ON CONFLICT(installation_id,event_key) DO NOTHING`, g.policy.InstallationID, eventKey, payload)
	if err != nil {
		return err
	}
	var sourceID, currentRevision string
	var available bool
	// New rows contain only this message's visible text. Nothing from private
	// issues, Q&A output or model assessments is written into this projection.
	_, err = tx.Exec(ctx, `INSERT INTO lark_knowledge_source(installation_id,chat_id,message_id,revision,message,body,available) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(installation_id,chat_id,message_id) DO NOTHING`, g.policy.InstallationID, g.policy.ChatID, m.MessageID, revision, payload, m.Body, !deleted)
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT id,revision,available FROM lark_knowledge_source WHERE installation_id=$1 AND chat_id=$2 AND message_id=$3 FOR UPDATE`, g.policy.InstallationID, g.policy.ChatID, m.MessageID).Scan(&sourceID, &currentRevision, &available)
	if err != nil {
		return err
	}
	if authoritative {
		_, err = tx.Exec(ctx, `UPDATE lark_knowledge_source SET revision=$2,message=$3,body=$4,available=$5,checked_at=now(),updated_at=now(),extracted=CASE WHEN revision=$2 THEN extracted ELSE '' END,digest=CASE WHEN revision=$2 THEN digest ELSE '' END WHERE id=$1`, sourceID, revision, payload, m.Body, !deleted)
		if err != nil {
			return err
		}
		currentRevision, available = revision, !deleted
	}
	if available && currentRevision == revision {
		var kind string
		if m.MessageType == "file" {
			var f struct {
				Key  string `json:"file_key"`
				Name string `json:"file_name"`
			}
			if json.Unmarshal([]byte(m.Content), &f) == nil && f.Key != "" {
				kind = "file"
			}
		} else if live && m.AddressedToBot && strings.TrimSpace(m.CommandBody) != "" {
			kind = "question"
		}
		if kind != "" {
			_, err = tx.Exec(ctx, `INSERT INTO lark_knowledge_job(installation_id,chat_id,job_key,source_id,revision,kind) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(installation_id,chat_id,job_key) DO UPDATE SET stage='received',state='{}',attempts=0,last_error='',available_at=now(),updated_at=now() WHERE $7 AND lark_knowledge_job.kind='file' AND lark_knowledge_job.stage='cancelled'`, g.policy.InstallationID, g.policy.ChatID, kind+":"+m.MessageID+":"+revision, sourceID, revision, kind, authoritative)
			if err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

type knowledgeJob struct {
	ID, SourceID, Revision, Kind, Stage string
	State                               knowledgeJobState
	Attempts                            int
	CreatedAt                           time.Time
}
type knowledgeJobState struct {
	FileID      string              `json:"file_id,omitempty"`
	Digest      string              `json:"digest,omitempty"`
	IssueID     string              `json:"issue_id,omitempty"`
	Reply       string              `json:"reply,omitempty"`
	SentID      string              `json:"sent_id,omitempty"`
	FirstSendAt time.Time           `json:"first_send_at,omitempty"`
	Evidence    []knowledgeEvidence `json:"evidence,omitempty"`
}

func (g *GroupKnowledge) nextJob(ctx context.Context) (knowledgeJob, error) {
	var j knowledgeJob
	var raw []byte
	err := g.pool.QueryRow(ctx, `SELECT id,source_id,revision,kind,stage,state,attempts,created_at FROM lark_knowledge_job WHERE installation_id=$1 AND chat_id=$2 AND available_at<=now() AND stage NOT IN ('complete','quarantined','cancelled') ORDER BY CASE kind WHEN 'ack' THEN 0 WHEN 'question' THEN 1 ELSE 2 END,created_at,id LIMIT 1`, g.policy.InstallationID, g.policy.ChatID).Scan(&j.ID, &j.SourceID, &j.Revision, &j.Kind, &j.Stage, &raw, &j.Attempts, &j.CreatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &j.State)
	}

	return j, err
}
func (g *GroupKnowledge) saveJob(ctx context.Context, j knowledgeJob, stage string) error {
	raw, err := json.Marshal(j.State)
	if err != nil {
		return err
	}
	_, err = g.pool.Exec(ctx, `UPDATE lark_knowledge_job SET stage=$2,state=$3,attempts=0,available_at=now(),last_error='',updated_at=now() WHERE id=$1`, j.ID, stage, raw)
	return err
}
func (g *GroupKnowledge) failJob(ctx context.Context, j knowledgeJob, err error) error {
	stage := j.Stage
	var terminal permanentKnowledgeError
	if errors.As(err, &terminal) || j.Attempts >= 7 {
		stage = "quarantined"
	}
	// Persist stable actionable categories only; HTTP errors can contain
	// tokens, document content or model output and must not enter logs.
	category := "transient processing failure; inspect service health and retry"
	if errors.As(err, &terminal) {
		category = terminal.Error()
	}
	backoff := time.Duration(1<<min(j.Attempts, 7)) * 15 * time.Second
	_, dbErr := g.pool.Exec(ctx, `UPDATE lark_knowledge_job SET stage=$2,attempts=attempts+1,available_at=now()+$3::interval,last_error=$4,updated_at=now() WHERE id=$1`, j.ID, stage, fmt.Sprintf("%d seconds", int(backoff.Seconds())), category)
	if stage == "quarantined" {
		g.logger.Warn("lark knowledge job requires action", "job_id", j.ID, "stage", j.Stage, "reason", category)
	}
	return dbErr
}
func (g *GroupKnowledge) source(ctx context.Context, id string) (knowledgeEvidence, error) {
	var e knowledgeEvidence
	var raw []byte
	err := g.pool.QueryRow(ctx, `SELECT id,revision,message,body || E'\n' || extracted,checked_at FROM lark_knowledge_source WHERE id=$1 AND installation_id=$2 AND chat_id=$3 AND available`, id, g.policy.InstallationID, g.policy.ChatID).Scan(&e.ID, &e.Revision, &raw, &e.Text, &e.CheckedAt)
	if err == nil {
		err = json.Unmarshal(raw, &e.Message)
	}
	return e, err
}
func (g *GroupKnowledge) invalidate(ctx context.Context, id string) error {
	_, err := g.pool.Exec(ctx, `UPDATE lark_knowledge_source SET available=false,checked_at=now(),updated_at=now() WHERE id=$1 AND installation_id=$2 AND chat_id=$3`, id, g.policy.InstallationID, g.policy.ChatID)
	return err
}
func (g *GroupKnowledge) search(ctx context.Context, question, exclude string) ([]knowledgeEvidence, error) {
	rows, err := g.pool.Query(ctx, `SELECT id,revision,message,body || E'\n' || extracted,checked_at FROM lark_knowledge_source WHERE installation_id=$1 AND chat_id=$2 AND available AND id<>$3::uuid AND to_tsvector('simple',body || ' ' || extracted) @@ websearch_to_tsquery('simple',$4) ORDER BY ts_rank(to_tsvector('simple',body || ' ' || extracted),websearch_to_tsquery('simple',$4)) DESC,updated_at DESC LIMIT 8`, g.policy.InstallationID, g.policy.ChatID, exclude, question)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []knowledgeEvidence
	for rows.Next() {
		var e knowledgeEvidence
		var raw []byte
		if err = rows.Scan(&e.ID, &e.Revision, &raw, &e.Text, &e.CheckedAt); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &e.Message); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (g *GroupKnowledge) reserveModelCall(ctx context.Context) error {
	var calls int
	err := g.pool.QueryRow(ctx, `INSERT INTO lark_knowledge_state(installation_id,workspace_id,model_calls) VALUES($1,$3,1) ON CONFLICT(installation_id) DO UPDATE SET model_day=CURRENT_DATE,model_calls=CASE WHEN lark_knowledge_state.model_day=CURRENT_DATE THEN lark_knowledge_state.model_calls+1 ELSE 1 END WHERE lark_knowledge_state.model_day<>CURRENT_DATE OR lark_knowledge_state.model_calls<$2 RETURNING model_calls`, g.policy.InstallationID, g.policy.DailyModelCalls, g.policy.WorkspaceID).Scan(&calls)
	if errors.Is(err, pgx.ErrNoRows) {
		return permanentKnowledgeError("daily model-call cap reached; retry after reviewing budget")
	}
	return err
}
func knowledgeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if len(name) > 180 {
		name = "upload" + strings.ToLower(filepath.Ext(name))
	}
	return name
}
