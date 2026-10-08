package lark

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

func messageTime(s string) time.Time {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(n)
}
func (g *GroupKnowledge) fromREST(m LarkMessage) InboundMessage {
	// History is source capture only. Reconciliation must not answer old
	// @mentions again; only receive events can enqueue questions.
	return InboundMessage{MessageAppLink: m.MessageAppLink, AppID: g.policy.AppID, EventType: "im.message.receive_v1", EventID: "history:" + m.MessageID, ChatID: ChatID(m.ChatID), ChatType: ChatTypeGroup, MessageID: m.MessageID, SenderOpenID: OpenID(m.SenderID), SenderType: m.SenderType, MessageType: m.MessageType, Content: m.Content, Body: flattenContent(m.MessageType, m.Content), CommandBody: flattenContent(m.MessageType, m.Content), CreateTime: m.CreateTime, ParentID: m.ParentID, RootID: m.RootID, ThreadID: m.ThreadID}
}

// verifySource fails closed for this operation on every fetch error. A
// transport outage is not a deletion: only authoritative absence/deleted data
// removes the current projection. The question and delivery paths call this
// even when a cached source was recently checked.
func (g *GroupKnowledge) verifySource(ctx context.Context, creds InstallationCredentials, e knowledgeEvidence) (bool, error) {
	items, err := g.api.GetMessage(ctx, creds, e.Message.MessageID)
	if err != nil {
		code := larkErrorCode(err)
		if code == 230110 || code == 230011 || code == 230050 {
			return false, g.invalidate(ctx, e.ID)
		}
		return false, err
	}
	for _, m := range items {
		if m.MessageID != e.Message.MessageID {
			continue
		}
		if m.ChatID != g.policy.ChatID || m.SenderType != "user" || m.Deleted {
			return false, g.invalidate(ctx, e.ID)
		}
		current := g.fromREST(m)
		if err = g.captureSource(ctx, current, true, false, false); err != nil {
			return false, err
		}
		return knowledgeRevision(current, false) == e.Revision, nil
	}
	return false, g.invalidate(ctx, e.ID)
}

func (g *GroupKnowledge) reconcile(ctx context.Context, inst Installation, creds InstallationCredentials) (result error) {
	started := g.now()
	// Reserve next run before HTTP. A crash never turns an unavailable API
	// into a one-second retry loop, and the watermark advances only on success.
	_, err := g.pool.Exec(ctx, `UPDATE lark_knowledge_state SET next_reconcile_at=now()+interval '15 minutes' WHERE installation_id=$1`, g.policy.InstallationID)
	if err != nil {
		return err
	}
	defer func() {
		final, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		defer cancel()
		if result == nil {
			_, result = g.pool.Exec(final, `UPDATE lark_knowledge_state SET history_through=$2,last_reconciled_at=now(),last_error='' WHERE installation_id=$1`, g.policy.InstallationID, started)
		} else {
			// Emit only a transition into failure, never routine unchanged logs.
			tag, e := g.pool.Exec(final, `UPDATE lark_knowledge_state SET last_error='reconciliation incomplete; watermark retained' WHERE installation_id=$1 AND last_error=''`, g.policy.InstallationID)
			if e == nil && tag.RowsAffected() > 0 {
				g.logger.Warn("lark knowledge reconciliation incomplete", "installation_id", g.policy.InstallationID)
			}
		}
	}()
	var through *time.Time
	if err = g.pool.QueryRow(ctx, `SELECT history_through FROM lark_knowledge_state WHERE installation_id=$1`, g.policy.InstallationID).Scan(&through); err != nil {
		return err
	}
	start := g.policy.StartTime
	if through != nil && through.Add(-time.Hour).After(start) {
		start = through.Add(-time.Hour)
	}
	threads := map[string]bool{}
	walk := func(thread string) error {
		token := ""
		seen := map[string]bool{}
		for page := 0; page < 200; page++ {
			got, err := g.api.KnowledgeHistory(ctx, creds, knowledgeHistoryParams{ChatID: ChatID(g.policy.ChatID), ThreadID: thread, PageToken: token, StartTime: start.Unix(), EndTime: started.Unix()})
			if err != nil {
				return err
			}
			for _, m := range got.Messages {
				if m.ChatID != g.policy.ChatID {
					continue
				}
				if m.ThreadID != "" {
					threads[m.ThreadID] = true
				}
				if m.SenderType != "user" {
					continue
				}
				if err = g.captureSource(ctx, g.fromREST(m), true, false, m.Deleted); err != nil {
					return err
				}
			}
			if !got.HasMore {
				return nil
			}
			if got.Next == "" || seen[got.Next] {
				return errors.New("knowledge history pagination incomplete")
			}
			seen[got.Next] = true
			token = got.Next
		}
		return errors.New("knowledge history page limit exceeded")
	}
	if err = walk(""); err != nil {
		return err
	}
	// Threads can receive new replies long after their root leaves the overlap.
	// Keep every known thread in the reconciliation set, not only recent roots.
	rows, err := g.pool.Query(ctx, `SELECT id,revision,message FROM lark_knowledge_source WHERE installation_id=$1 AND chat_id=$2 ORDER BY id`, g.policy.InstallationID, g.policy.ChatID)
	if err != nil {
		return err
	}
	var known []knowledgeEvidence
	for rows.Next() {
		var e knowledgeEvidence
		var raw []byte
		if err = rows.Scan(&e.ID, &e.Revision, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &e.Message); err != nil {
			rows.Close()
			return err
		}
		known = append(known, e)
		if e.Message.ThreadID != "" {
			threads[e.Message.ThreadID] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for thread := range threads {
		if err = walk(thread); err != nil {
			return err
		}
	}
	// REST re-fetch also covers edits/recalls that the current receive-only
	// subscription does not deliver. No new app consumer/subscription is needed.
	for _, e := range known {
		if _, err = g.verifySource(ctx, creds, e); err != nil {
			// A transport/permission error is not authoritative deletion.
			// Queries re-fetch every source and fail closed while access is
			// unavailable; keep the watermark so reconciliation retries.
			return err
		}
	}
	return nil
}
