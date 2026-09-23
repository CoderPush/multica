---
name: nanohome-delivery
description: Coordinates concise NanoHome briefs and explicit engineering, QA and release handoffs. Use when a NanoHome agent reads, updates, delegates, reviews or closes a delivery issue.
---
# NanoHome delivery

## Scope and authority
Applies to Nanohome.vn and orchestars/nanohome-web plus orchestars/nanohome-medusa. Read repository AGENTS.md and the issue's current acceptance and holds. This skill consolidates existing delivery authority; it grants no new access or production-data permission. Squad instructions reach the leader only, so each specialist must load this assigned skill.

Routine tested, independently reviewed, reversible fixes may proceed through the repositories' authorized merge and normal deployment path without another generic maintainer decision. Exact-head checks, required signoffs and action-specific human holds still govern. Before any merge, release, checkout test or production action, read [release boundaries](references/release-boundaries.md). A model, agent role or green CI result never grants authority.

## Model and role selection
| Role | Configured model / effort | Responsibility |
| --- | --- | --- |
| NanoHome Lead | GPT-6 Luna / high | Select ready work, own capacity and next-owner handoffs; do not implement |
| Frontend / Backend Engineer | GPT-6 Sol / medium | Implement in the assigned repository and repair CI/review findings |
| QA Engineer | GPT-6 Sol / medium | Independently review the exact PR head and verify affected behavior |
| Platform Engineer | GPT-6 Sol / medium | Resolve assigned CI/runtime blockers and verify authorized deployments |
| Mika | GPT-6 Luna / high | Priorities, exceptional decisions and advisory audit; no delivery dispatch |

Use explicit agent settings and standard service. Do not inherit a runtime default or silently switch models. For unavailable models/auth, record the actual runtime/role error and owner once; do not repeat inference retries, change credentials or spend reset credits. Escalate a hard or repeatedly failed reasoning task with evidence and a proposed model/effort change; settings changes require separate authorization.

## Keep issues short
Start with the current brief and latest relevant result. Read older history only for a concrete uncertainty. Keep outcome, ticket-specific acceptance/holds, PR/head and evidence links, and one next action/owner in the description. Aim for 150–250 words, allowing more for real acceptance complexity. Update superseded status text; preserve decision links and concurrent edits. Generic procedure belongs here, never copied into every issue.

Use `--no-start` for bookkeeping. Do not change assignees/status merely to tidy prose. Comments normally take 2–5 sentences and must add a decision, evidence, actionable finding, blocker or handoff. Do not narrate unchanged status, restate policy or mention agents for housekeeping. Put large evidence in linked artifacts.

## Stage ownership
| Stage | Accountable owner | Exit evidence and next owner |
| --- | --- | --- |
| Intake / readiness | Lead | Outcome, acceptance, repository, authority and dependencies clear → named Engineer |
| Implementation | Named Engineer | PR/current head, relevant checks, screenshots when useful, unresolved decisions → Lead assigns QA once |
| Independent review | QA | Verdict bound to exact head → original Engineer for findings, or release owner for approved work |
| Rework | Original Engineer | Changed diff and refreshed checks → same QA issue; new head invalidates old approval |
| Merge / release | Named Engineer; Platform only when assigned | Required checks/signoff and authority satisfied, deployment run/commit linked → named verifier |
| Acceptance / close | QA or assigned independent verifier; Lead owns parent acceptance | Affected live behavior and ticket acceptance verified → close scoped deliverable; preserve remaining parent gates |

Lead alone allocates workers and capacity. Specialists return a compact result to Lead; they do not dispatch additional workers. Predeclare the next owner in the brief. Do not add a ceremonial Lead approval after an already-authorized stage. Lead reconciles existing child-completion/assignment triggers before making any new dispatch.

Backlog parks unready/unselected work; Todo is ready; In Progress includes engineering, agent review and release verification. Reserve In Review for a named human decision, not ordinary agent review. Blocked names each dependency, owner and clearing evidence separately. Done requires scoped acceptance, not merely a successful run. NANO-2 remains ongoing coordination while delivery continues.

## One handoff, one pipeline
Check queued/running tasks before each dispatch. Assignment outside backlog, promotion from backlog and child completion may already start the executor. Choose one intentional trigger; do not also mention/start them. If a separate-thread handoff returns `already_active`, it was refused, not delivered. Record a checkpoint for the next reconciliation; do not retry with another thread. Existing-thread supplements may follow normal routing.

Keep two executable delivery pipelines across both repos, including queued QA/rework; a parent and its review child count as one. One task per specialist, one coordinator. Park purely external/human blockers while preserving ownership and next action; reacquire capacity before resuming. Never cancel valid runs to make space. Reuse one review child per PR across changed heads. Close an independently accepted child without pretending its parent/release is complete.

Resume from the last verified checkpoint. A handoff contains PR/head, change since the last verdict, evidence, next owner/action and remaining blockers. Example: “PR #X at abc123: fixed the search count. Checks [link] pass. QA: inspect this delta on the existing review issue; catalog migration remains separately blocked on Ian.”

## Bounded follow-through
Advance CI/review repair, authorized release and verification before starting more work. Existing hourly daytime coordination owns follow-through; consult its saved trigger rather than inventing schedules. Refresh broad backlog at most hourly. Inspect compact summaries first and expand at most three changed/stalled items per pass. Do not poll, wait for workers, create scan-only issues or add watchers. An unchanged blocker means stop quietly. Pending deployment needs its run URL, verifier and next check; it is not Done.

Lead runs the daily read-only discovery and weekly outcome review through existing capacity: read [proactive checks](references/proactive-checks.md). Mika's separate audit remains advisory.

Only Lead handles authorized Lark delivery updates: shipped/verified outcome, new verification failure or concrete decision needed. Link each issue on first mention using its resolved HTTPS `/nano/issues/<uuid>` URL and link PRs. No transport receipt means no claim of delivery. Keep support conversations separate: read [NanoHome Support](references/support.md) before responding there.
