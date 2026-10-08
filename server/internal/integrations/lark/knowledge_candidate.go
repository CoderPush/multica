package lark

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/issueproperty"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const knowledgeBlockStart = "<!-- tarley:assessment:v1 -->"
const knowledgeBlockEnd = "<!-- /tarley:assessment:v1 -->"

func knowledgeMergeAssessment(existing, body string) (string, error) {
	start, end := strings.Index(existing, knowledgeBlockStart), strings.Index(existing, knowledgeBlockEnd)
	block := knowledgeBlockStart + "\n" + body + "\n" + knowledgeBlockEnd
	if start < 0 && end < 0 {
		return strings.TrimSpace(existing) + "\n\n" + block, nil
	}
	if start < 0 || end < start || strings.Count(existing, knowledgeBlockStart) != 1 || strings.Count(existing, knowledgeBlockEnd) != 1 {
		return "", permanentKnowledgeError("assessment section was edited ambiguously; preserve it for human reconciliation")
	}
	return existing[:start] + block + existing[end+len(knowledgeBlockEnd):], nil
}
func knowledgeEmailDigest(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(s))))
	return hex.EncodeToString(sum[:])
}

type knowledgeCandidate struct {
	ID, Title string
	Metadata  []byte
}

func (g *GroupKnowledge) candidateMatch(ctx context.Context, j knowledgeJob, a knowledgeAssessment, src knowledgeEvidence) (string, error) {
	rows, err := g.pool.Query(ctx, `SELECT id,title,metadata FROM issue WHERE workspace_id=$1 AND project_id=$2 AND (origin_type='lark_chat' AND origin_id=$3 OR metadata->>'cv_source_message'=$4 OR metadata->>'cv_sha256'=$5 OR metadata->>'tarley_email_sha256'=NULLIF($6,'') OR metadata->'tarley_source_messages' ? $4 OR strpos(description,$7)>0)`, g.policy.WorkspaceID, g.policy.ProjectID, j.State.FileID, src.Message.MessageID, j.State.Digest, knowledgeEmailDigest(a.Email), "cv_source_message: "+src.Message.MessageID)
	if err != nil {
		return "", err
	}
	var matches []string
	for rows.Next() {
		var c knowledgeCandidate
		if err = rows.Scan(&c.ID, &c.Title, &c.Metadata); err != nil {
			rows.Close()
			return "", err
		}
		if knowledgeName(c.Title) != knowledgeName(a.Name) {
			rows.Close()
			return "", permanentKnowledgeError("candidate identity conflicts with the matched record name; human clarification required")
		}
		matches = append(matches, c.ID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	if len(matches) > 1 {
		return "", permanentKnowledgeError("candidate identity matches multiple records; human clarification required")
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	// A name alone cannot prove two CVs describe the same person. A possible
	// match is held rather than merged or turned into a duplicate candidate.
	rows, err = g.pool.Query(ctx, `SELECT title FROM issue WHERE workspace_id=$1 AND project_id=$2`, g.policy.WorkspaceID, g.policy.ProjectID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var title string
		if err = rows.Scan(&title); err != nil {
			return "", err
		}
		if knowledgeName(title) == knowledgeName(a.Name) {
			return "", permanentKnowledgeError("candidate name already exists without a reliable identity match; human clarification required")
		}
	}
	return "", rows.Err()
}

func (g *GroupKnowledge) updateCandidate(ctx context.Context, j knowledgeJob, src knowledgeEvidence) (string, error) {
	a, cachedID, err := g.assessment(ctx, j.State.FileID)
	if err != nil {
		return "", err
	}
	if !a.IsCV {
		return "", nil
	}
	allowed, err := g.tasks.MemberMayInvokeAgent(ctx, knowledgeUUID(g.policy.AgentID), knowledgeUUID(g.policy.OwnerID))
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", permanentKnowledgeError("private service owner may no longer invoke the configured agent")
	}
	issueID := cachedID
	if cachedID == "" {
		issueID, err = g.candidateMatch(ctx, j, a, src)
	}
	if err != nil {
		return "", err
	}
	if cachedID != "" {
		existing, err := db.New(g.pool).GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{ID: knowledgeUUID(cachedID), WorkspaceID: knowledgeUUID(g.policy.WorkspaceID)})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if errors.Is(err, pgx.ErrNoRows) || uuidString(existing.ProjectID) != g.policy.ProjectID {
			return "", permanentKnowledgeError("previous candidate record changed or was deleted; human reconciliation required")
		}
		if knowledgeName(existing.Title) != knowledgeName(a.Name) {
			return "", permanentKnowledgeError("candidate identity conflicts with the matched record name; human clarification required")
		}
		issueID = cachedID
	}
	if knowledgeSourceURL(src.Message) == "" {
		return "", permanentKnowledgeError("original message link unavailable; reconcile source before updating candidate")
	}
	body := knowledgeAssessmentBody(a, src.Message)
	// The existing laptop fallback searches source markers before repeating a
	// create. This marker is committed with the issue, before later metadata.
	body += "\n<!-- cv_source_message: " + src.Message.MessageID + " -->\n"
	if issueID == "" {
		properties, err := g.candidateProperties(ctx, nil, true)
		if err != nil {
			return "", err
		}
		result, err := g.issues.Create(ctx, service.IssueCreateParams{WorkspaceID: knowledgeUUID(g.policy.WorkspaceID), ProjectID: knowledgeUUID(g.policy.ProjectID), Title: a.Name, Description: pgtype.Text{String: knowledgeBlockStart + "\n" + body + knowledgeBlockEnd, Valid: true}, Status: "backlog", Priority: "none", AssigneeType: pgtype.Text{String: "member", Valid: true}, AssigneeID: knowledgeUUID(g.policy.OwnerID), CreatorType: "agent", CreatorID: knowledgeUUID(g.policy.AgentID), OriginType: pgtype.Text{String: "lark_chat", Valid: true}, OriginID: knowledgeUUID(j.State.FileID), Properties: properties}, service.IssueCreateOpts{ActorID: g.policy.AgentID, Platform: "lark"})
		if errors.Is(err, service.ErrActiveDuplicate) {
			return "", permanentKnowledgeError("candidate create conflicted; reconcile identity before retrying")
		}
		if err != nil {
			return "", err
		}
		issueID = uuidString(result.Issue.ID)
	}
	// Stable object and attachment identities reconcile a lost upload/DB reply.
	// Originals are immutable and kept even if parsing/assessment later fails.
	var filename string
	var original []byte
	if err = g.pool.QueryRow(ctx, `SELECT filename,original FROM lark_knowledge_file WHERE id=$1 AND installation_id=$2 AND chat_id=$3`, j.State.FileID, g.policy.InstallationID, g.policy.ChatID).Scan(&filename, &original); err != nil {
		return "", err
	}
	fileUUID, err := uuid.Parse(j.State.FileID)
	if err != nil {
		return "", err
	}
	attachmentID := uuid.NewSHA1(fileUUID, []byte(issueID)).String()
	key := "lark-knowledge/" + g.policy.WorkspaceID + "/" + j.State.Digest
	contentType := "application/pdf"
	if strings.HasSuffix(strings.ToLower(filename), ".docx") {
		contentType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	}
	url, err := g.storage.Upload(ctx, key, original, contentType, filename)
	if err != nil {
		return "", err
	}
	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	q := db.New(tx)
	issue, err := q.LockIssueForDescriptionUpdate(ctx, db.LockIssueForDescriptionUpdateParams{ID: knowledgeUUID(issueID), WorkspaceID: knowledgeUUID(g.policy.WorkspaceID)})
	if err != nil {
		return "", err
	}
	if uuidString(issue.ProjectID) != g.policy.ProjectID {
		return "", permanentKnowledgeError("candidate moved out of the configured project; human reconciliation required")
	}
	if knowledgeName(issue.Title) != knowledgeName(a.Name) {
		return "", permanentKnowledgeError("candidate identity changed during upload; human clarification required")
	}
	// Lock and recheck the source immediately before private mutations.
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT available AND revision=$2 FROM lark_knowledge_source WHERE id=$1 FOR SHARE`, src.ID, src.Revision).Scan(&valid); err != nil {
		return "", err
	}
	if !valid {
		return "", permanentKnowledgeError("candidate source changed during processing; reconcile current source")
	}
	properties, err := g.candidateProperties(ctx, tx, false)
	if err != nil {
		return "", err
	}
	propertyMap := map[string]json.RawMessage{}
	for id, value := range properties {
		propertyMap[uuidString(id)] = value
	}
	propertyJSON, _ := json.Marshal(propertyMap)
	merged, err := knowledgeMergeAssessment(issue.Description.String, body)
	if err != nil {
		return "", err
	}
	link := "!file[Original CV](/api/attachments/" + attachmentID + "/download)"
	if !strings.Contains(merged, "/api/attachments/"+attachmentID+"/download") {
		merged += "\n\n" + link
	}
	_, err = tx.Exec(ctx, `INSERT INTO attachment(id,workspace_id,issue_id,uploader_type,uploader_id,filename,url,content_type,size_bytes) VALUES($1,$2,$3,'agent',$4,$5,$6,$7,$8) ON CONFLICT(id) DO NOTHING`, attachmentID, g.policy.WorkspaceID, issueID, g.policy.AgentID, filename, url, contentType, len(original))
	if err != nil {
		return "", err
	}
	metadata := map[string]any{}
	if len(issue.Metadata) > 0 {
		if err = json.Unmarshal(issue.Metadata, &metadata); err != nil {
			return "", err
		}
	}
	messages := []string{}
	if raw, ok := metadata["tarley_source_messages"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				messages = append(messages, s)
			}
		}
	}
	found := false
	for _, id := range messages {
		if id == src.Message.MessageID {
			found = true
		}
	}
	if !found {
		messages = append(messages, src.Message.MessageID)
	}
	if len(messages) > 100 {
		return "", permanentKnowledgeError("candidate provenance limit reached; human consolidation required")
	}
	metadata["cv_source_message"] = src.Message.MessageID
	metadata["cv_sha256"] = j.State.Digest
	metadata["tarley_source_messages"] = messages
	metadata["tarley_jd_version"] = knowledgeJDVersion
	if a.Email != "" {
		metadata["tarley_email_sha256"] = knowledgeEmailDigest(a.Email)
	}
	metaJSON, _ := json.Marshal(metadata)
	_, err = tx.Exec(ctx, `UPDATE issue SET description=$3,metadata=$4,properties=properties || $5::jsonb,revision=revision+1,updated_at=now() WHERE id=$1 AND workspace_id=$2`, issueID, g.policy.WorkspaceID, merged, metaJSON, propertyJSON)
	if err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `UPDATE lark_knowledge_assessment SET issue_id=$2 WHERE file_id=$1`, j.State.FileID, issueID)
	if err != nil {
		return "", err
	}
	if err = tx.Commit(ctx); err != nil {
		return "", err
	}
	g.issues.PublishAttachmentsChanged(ctx, issue, knowledgeUUID(g.policy.AgentID))
	return issueID, nil
}

// Only factual review properties change. Hiring stage is initialized on create
// and is never changed on an existing issue, even on a subsequent CV revision.
func (g *GroupKnowledge) candidateProperties(ctx context.Context, tx pgx.Tx, initial bool) (map[pgtype.UUID]json.RawMessage, error) {
	wanted := map[string]string{"Record type": "Candidate", "Review status": "Assessed", "Assessment version": knowledgeJDVersion}
	if initial {
		wanted["Hiring stage"] = "New"
	}
	query := `SELECT id,name,type,config FROM issue_property WHERE workspace_id=$1 AND archived_at IS NULL`
	var rows pgx.Rows
	var err error
	if tx != nil {
		rows, err = tx.Query(ctx, query+" FOR SHARE", g.policy.WorkspaceID)
	} else {
		rows, err = g.pool.Query(ctx, query, g.policy.WorkspaceID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[pgtype.UUID]json.RawMessage{}
	for rows.Next() {
		var def db.IssueProperty
		if err = rows.Scan(&def.ID, &def.Name, &def.Type, &def.Config); err != nil {
			return nil, err
		}
		value, ok := wanted[def.Name]
		if !ok {
			continue
		}
		if def.Type == "select" {
			var config struct{ Options []struct{ ID, Name string } }
			if err = json.Unmarshal(def.Config, &config); err != nil {
				return nil, err
			}
			found := false
			for _, o := range config.Options {
				if o.Name == value {
					value = o.ID
					found = true
					break
				}
			}
			if !found {
				return nil, permanentKnowledgeError("required hiring property option missing")
			}
		}
		raw, _ := json.Marshal(value)
		canonical, err := issueproperty.ValidateValue(def, raw)
		if err != nil {
			return nil, permanentKnowledgeError("required hiring property schema changed")
		}
		out[def.ID] = canonical
		delete(wanted, def.Name)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(wanted) > 0 {
		return nil, permanentKnowledgeError("required hiring properties missing; configure the existing hiring board")
	}
	return out, nil
}
