package handler

import (
	"context"
	"fmt"
	"testing"

	"github.com/multica-ai/multica/server/internal/testutil"
)

func TestAgentMentionDoesNotRedispatchActiveAssignee(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{"queued", "dispatched", "running", "waiting_local_directory", "completed"} {
		t.Run(status, func(t *testing.T) {
			runtimeID := dbfx.Runtime(t, "handoff runtime")
			agentID := dbfx.Agent(t, "executor", runtimeID)
			coordinatorID := dbfx.Agent(t, "coordinator", runtimeID)
			issueID := dbfx.Issue(t, "delivery", testutil.Cols{"assignee_type": "agent", "assignee_id": agentID})
			rootID := dbfx.Comment(t, issueID, "existing delivery handoff")
			dbfx.Task(t, agentID, testutil.Cols{"runtime_id": runtimeID, "issue_id": issueID, "status": status, "trigger_comment_id": rootID, "comment_thread_id": rootID})
			issue, err := testHandler.Queries.GetIssue(ctx, parseUUID(issueID))
			if err != nil {
				t.Fatal(err)
			}
			content := fmt.Sprintf("[@Executor](mention://agent/%s) continue delivery", agentID)
			triggers, targets := testHandler.computeCommentAgentTriggers(ctx, issue, content, nil, "agent", coordinatorID, commentTriggerComputeOptions{OriginatorUserID: testUserID})
			if status != "completed" {
				if len(triggers) != 0 || len(targets) != 1 || targets[0].ReasonCode != ReasonAlreadyActive {
					t.Fatalf("active assignee should be refused explicitly: triggers=%+v targets=%+v", triggers, targets)
				}
			} else if len(triggers) != 1 {
				t.Fatalf("completed executor must accept a new handoff: %+v", targets)
			}
			root, err := testHandler.Queries.GetComment(ctx, parseUUID(rootID))
			if err != nil {
				t.Fatal(err)
			}
			triggers, _ = testHandler.computeCommentAgentTriggers(ctx, issue, content, &root, "agent", coordinatorID, commentTriggerComputeOptions{OriginatorUserID: testUserID})
			if len(triggers) != 1 {
				t.Fatal("supplements in the active thread must retain normal routing")
			}
			triggers, _ = testHandler.computeCommentAgentTriggers(ctx, issue, content, nil, "member", testUserID, commentTriggerComputeOptions{OriginatorUserID: testUserID})
			if len(triggers) != 1 {
				t.Fatal("human follow-up must retain normal routing")
			}
			issue.AssigneeID = parseUUID(coordinatorID)
			triggers, _ = testHandler.computeCommentAgentTriggers(ctx, issue, content, nil, "agent", coordinatorID, commentTriggerComputeOptions{OriginatorUserID: testUserID})
			if len(triggers) != 1 {
				t.Fatal("mention of a separate specialist must retain normal routing")
			}
		})
	}
}
