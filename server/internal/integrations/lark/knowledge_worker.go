package lark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
)

func (g *GroupKnowledge) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// One bounded step per tick. A session advisory lock serializes all
			// replicas; the read-only workspace fence below prevents teardown races.
			if err := g.tick(ctx); err != nil && ctx.Err() == nil {
				g.logger.Debug("lark knowledge step deferred", "reason", "database or installation unavailable")
			}
		}
	}
}
func (g *GroupKnowledge) tick(ctx context.Context) error {
	parent := ctx
	budget := g.stepTimeout
	if budget == 0 {
		budget = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	conn, err := g.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	key := "lark-knowledge:" + g.policy.InstallationID
	var acquired bool
	if err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtextextended($1,0))`, key).Scan(&acquired); err != nil || !acquired {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := conn.Exec(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1,0))`, key); err != nil {
			_ = conn.Conn().Close(cleanup)
		}
	}()
	// Keep teardown from removing installation-owned rows midway through a
	// bounded step. This transaction only holds a workspace read lock; jobs
	// commit independently so a crash does not discard completed work.
	fence, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer fence.Rollback(context.WithoutCancel(ctx))
	var workspace string
	if err = fence.QueryRow(ctx, `SELECT id FROM workspace WHERE id=$1 FOR KEY SHARE`, g.policy.WorkspaceID).Scan(&workspace); err != nil {
		return err
	}
	inst, creds, err := g.activeCredentials(ctx)
	if err != nil {
		return err
	}
	var due bool
	err = g.pool.QueryRow(ctx, `INSERT INTO lark_knowledge_state(installation_id,workspace_id) VALUES($1,$2) ON CONFLICT(installation_id) DO UPDATE SET installation_id=EXCLUDED.installation_id RETURNING next_reconcile_at<=now()`, g.policy.InstallationID, g.policy.WorkspaceID).Scan(&due)
	if err != nil {
		return err
	}
	if due {
		return g.reconcile(ctx, inst, creds)
	}
	if g.policy.Mode != "process" {
		return nil
	}
	j, err := g.nextJob(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = g.step(ctx, creds, j); err != nil {
		// A shutdown leaves the durable stage for the next worker. Our own
		// deadline is a failed attempt and must consume retry/backoff budget.
		if parent.Err() != nil {
			return err
		}
		failureCtx, cancelFailure := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancelFailure()
		return g.failJob(failureCtx, j, err)
	}
	return nil
}
func (g *GroupKnowledge) activeCredentials(ctx context.Context) (Installation, InstallationCredentials, error) {
	inst, err := g.installations.GetLarkInstallationByAppID(ctx, g.policy.AppID)
	if err != nil {
		return Installation{}, InstallationCredentials{}, err
	}
	if inst.Status != string(InstallationActive) || uuidString(inst.ID) != g.policy.InstallationID || uuidString(inst.WorkspaceID) != g.policy.WorkspaceID || uuidString(inst.AgentID) != g.policy.AgentID || uuidString(inst.InstallerUserID) != g.policy.OwnerID {
		return Installation{}, InstallationCredentials{}, errors.New("knowledge installation policy no longer matches")
	}
	member, err := g.installations.IsWorkspaceMember(ctx, inst.WorkspaceID, knowledgeUUID(g.policy.OwnerID))
	if err != nil {
		return inst, InstallationCredentials{}, err
	}
	if !member {
		return inst, InstallationCredentials{}, errors.New("knowledge service owner is no longer a workspace member")
	}
	creds, err := installationCredentialsFor(inst, g.credentials)
	return inst, creds, err
}
func (g *GroupKnowledge) step(ctx context.Context, creds InstallationCredentials, j knowledgeJob) error {
	// Operators can reconcile job JSON. Invalid references quarantine this job
	// rather than reaching a trusted UUID parser or blocking the whole queue.
	for _, id := range []string{j.State.FileID, j.State.IssueID} {
		if id != "" {
			if _, err := util.ParseUUID(id); err != nil {
				return permanentKnowledgeError("invalid persisted job UUID; reconcile job state")
			}
		}
	}
	src, err := g.source(ctx, j.SourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return g.saveJob(ctx, j, "cancelled")
	}
	if err != nil {
		return err
	}
	if src.Revision != j.Revision {
		return g.saveJob(ctx, j, "cancelled")
	}
	if j.Kind == "ack" || j.Stage == "reply-ready" {
		return g.deliver(ctx, creds, j, src)
	}
	if j.Kind == "question" {
		return g.question(ctx, creds, j, src)
	}
	switch j.Stage {
	case "received":
		current, err := g.verifySource(ctx, creds, src)
		if err != nil {
			return err
		}
		if !current {
			return g.saveJob(ctx, j, "cancelled")
		}
		var file struct {
			Key  string `json:"file_key"`
			Name string `json:"file_name"`
		}
		if json.Unmarshal([]byte(src.Message.Content), &file) != nil || file.Key == "" {
			return permanentKnowledgeError("upload metadata is invalid")
		}
		ext := strings.ToLower(filepath.Ext(file.Name))
		if ext != ".pdf" && ext != ".docx" {
			return permanentKnowledgeError("unsupported file format; only PDF and DOCX are indexed")
		}
		stream, err := g.api.DownloadMessageResourceStream(ctx, creds, DownloadResourceParams{MessageID: src.Message.MessageID, FileKey: file.Key, Type: "file"})
		if err != nil {
			return err
		}
		defer stream.Body.Close()
		if stream.SizeBytes > knowledgeMaxFileBytes {
			return permanentKnowledgeError("file exceeds 20 MiB; provide a smaller source")
		}
		data, err := io.ReadAll(io.LimitReader(stream.Body, knowledgeMaxFileBytes+1))
		if err != nil {
			return err
		}
		if len(data) > knowledgeMaxFileBytes {
			return permanentKnowledgeError("file exceeds 20 MiB; provide a smaller source")
		}
		digest := sha256.Sum256(data)
		j.State.Digest = hex.EncodeToString(digest[:])
		err = g.pool.QueryRow(ctx, `INSERT INTO lark_knowledge_file(installation_id,chat_id,digest,filename,original) VALUES($1,$2,$3,$4,$5) ON CONFLICT(installation_id,chat_id,digest) DO UPDATE SET digest=EXCLUDED.digest RETURNING id`, g.policy.InstallationID, g.policy.ChatID, j.State.Digest, knowledgeFilename(file.Name), data).Scan(&j.State.FileID)
		if err != nil {
			return err
		}
		return g.saveJob(ctx, j, "downloaded")
	case "downloaded":
		var filename, text string
		var data []byte
		err = g.pool.QueryRow(ctx, `SELECT filename,original,extracted FROM lark_knowledge_file WHERE id=$1 AND installation_id=$2 AND chat_id=$3`, j.State.FileID, g.policy.InstallationID, g.policy.ChatID).Scan(&filename, &data, &text)
		if err != nil {
			return err
		}
		if text == "" {
			text, err = g.extract(ctx, filename, data)
			if err != nil {
				return err
			}
			if len(text) > knowledgeMaxTextBytes {
				return permanentKnowledgeError("extracted text limit exceeded")
			}
			_, err = g.pool.Exec(ctx, `UPDATE lark_knowledge_file SET extracted=$2 WHERE id=$1`, j.State.FileID, text)
			if err != nil {
				return err
			}
		}
		_, err = g.pool.Exec(ctx, `UPDATE lark_knowledge_source SET extracted=$2,digest=$3 WHERE id=$1 AND revision=$4 AND available`, j.SourceID, text, j.State.Digest, j.Revision)
		if err != nil {
			return err
		}
		return g.saveJob(ctx, j, "extracted")
	case "extracted":
		if err = g.classify(ctx, j); err != nil {
			return err
		}
		return g.saveJob(ctx, j, "classified")
	case "classified":
		current, err := g.verifySource(ctx, creds, src)
		if err != nil {
			return err
		}
		if !current {
			return g.saveJob(ctx, j, "cancelled")
		}
		src, err = g.source(ctx, j.SourceID)
		if err != nil {
			return err
		}
		issueID, err := g.updateCandidate(ctx, j, src)
		if err != nil {
			return err
		}
		j.State.IssueID = issueID
		return g.saveJob(ctx, j, "candidate-updated")
	case "candidate-updated":
		// The digest, rather than message id, deduplicates same-file reuploads.
		// Historical backfill is silent. Outage recovery after start_time uses
		// the same keys as events and still produces at most one logical ACK.
		if messageTime(src.Message.CreateTime).Before(g.policy.StartTime) {
			return g.saveJob(ctx, j, "complete")
		}
		state, _ := json.Marshal(knowledgeJobState{Reply: "Received. The file has been captured for the CTO search."})
		_, err = g.pool.Exec(ctx, `INSERT INTO lark_knowledge_job(installation_id,chat_id,job_key,source_id,revision,kind,state) VALUES($1,$2,$3,$4,$5,'ack',$6) ON CONFLICT(installation_id,chat_id,job_key) DO NOTHING`, g.policy.InstallationID, g.policy.ChatID, "ack:"+j.State.Digest, j.SourceID, j.Revision, state)
		if err != nil {
			return err
		}
		return g.saveJob(ctx, j, "acknowledgement-queued")
	case "acknowledgement-queued":
		var stage string
		err = g.pool.QueryRow(ctx, `SELECT stage FROM lark_knowledge_job WHERE installation_id=$1 AND chat_id=$2 AND job_key=$3`, g.policy.InstallationID, g.policy.ChatID, "ack:"+j.State.Digest).Scan(&stage)
		if err != nil {
			return err
		}
		if stage == "complete" {
			return g.saveJob(ctx, j, "acknowledgement-sent")
		}
		if stage == "cancelled" {
			return g.saveJob(ctx, j, "complete")
		}
		if stage == "quarantined" {
			return permanentKnowledgeError("upload acknowledgement requires thread reconciliation")
		}
		// Yield to the outbox even though this source job is older.
		_, err = g.pool.Exec(ctx, `UPDATE lark_knowledge_job SET available_at=now()+interval '10 seconds' WHERE id=$1`, j.ID)
		return err
	case "acknowledgement-sent":
		return g.saveJob(ctx, j, "complete")
	default:
		return permanentKnowledgeError("unknown processing stage; inspect migration compatibility")
	}
}
