---
name: web-delivery
description: Keeps CoderPush Web tickets concise and moves authorized changes through delivery. Use when reading, updating, handing off, reviewing or closing a Web Factory issue.
---
# Web delivery

## Read only what the next decision needs
Start with the current brief and latest relevant decision/result. Open older threads only to resolve a specific uncertainty. Reuse recorded evidence unless the code, scope or environment changed. An issue is a work brief, not an agent transcript or operating manual.

## Keep the ticket useful
Aim for 150–250 words; complex acceptance may need more. Write:
- Desired outcome and ticket-specific acceptance.
- Current result with a few source links.
- One next action and owner, or a concrete blocker and decision owner.

Preserve ticket-specific constraints and explicit pauses. Replace superseded status paragraphs; never append “historical reconciliation” sections. History belongs in existing comments, runs and linked artifacts. Do not copy these instructions, generic release checklists or runtime incident narratives into every ticket. Reread before saving; preserve concurrent edits. Use --no-start for bookkeeping and never change ownership/status solely to tidy prose.

Comments should change what someone knows or does: a decision, evidence, actionable finding, blocker or handoff. Usually 2–5 sentences with links. Do not repeat the description, narrate unchanged status, restate the whole policy, add ceremonial approval requests or notify by mention for housekeeping. Put large technical evidence in an artifact and link it.

## Handoff once
Check queued/running work before dispatch. Assignment outside backlog or promotion from backlog may already start a run; do not also mention or explicitly start the executor. Reuse one review child per PR, including changed heads. Count the delivery parent and review child as one item against the squad's two active delivery limit.

Backlog parks work. Todo is ready. In Progress includes engineering, agent review and release verification. In Review means a specific human decision, with that person and question named. Done means acceptance is met. Do not manufacture a human approval requirement for an agent review or a completed bookkeeping audit. Preserve explicitly required human acceptance.

## Finish authorized work
The current Web Factory squad policy governs release authority. Routine approved code/copy/layout work follows the fast lane: clean exact-head agent review, passing required checks, guarded merge by Engineer, then verified automatic production and affected behavior. A second Lead or human approval is unnecessary. Reviewer does not merge. Lead coordinates and does not implement.

Changes to claims, URLs/removals, dependencies, tracking, sensitive data, credentials or infrastructure retain their named exception approvals; new marketing direction belongs to Anh and consequential technical decisions to Harley. An explicit pause or inspection-only task always wins. Never push main directly, force-push, bypass checks, change credentials or manually deploy without authorization.

Use [browser evidence](references/browser-evidence.md) when collecting or reviewing visual/CI evidence. Keep ticket-specific viewport and route requirements in the ticket. Code-only changes need relevant tests, not invented visual work.

Only Web Lead sends an authorized originating-thread milestone: read [Lark delivery](references/lark-delivery.md) before sending. Other roles return their result on the issue. No receipt means no delivered claim.

## Engineer handoff contract
Engineer owns authorized work through verified release. Before opening review, include the issue key in the PR title and link the PR to the issue. Supply one compact handoff: PR/current head, what changed since the previous verdict, checks and evidence links, the requested action, and any unresolved decision. Read [engineering handoffs](references/engineering-handoffs.md) for checkpoints and blocker handling.

## Stop when nothing changed
Known quota/auth/capability blockers without new evidence do not justify a rerun. State the actual error, owner and next verification once. Do not spend reset credits, change model/auth settings, poll or keep an agent alive waiting. Scheduled checks use compact summaries, expand at most three changed/stalled items, and select at most one eligible backlog item. No actionable change means stop quietly. Existing monitors handle the next pass.

## Example
“Medtech is live from PR #200. Regression and production verification passed [links]. The route returns 200 and the industry URL redirects correctly. No remaining action.”
This is enough for a completed delivery; the PR and evidence contain the technical details.
