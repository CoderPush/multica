# Engineering handoffs

Engineer owns implementation through verified release within the authorized scope. Lead is needed for a real scope, priority or authority decision, not for each transition.

## Prepare review once
Include the issue key in the PR title when creating it. Confirm linking before requesting review. Fix small metadata omissions directly using the authorized CLI; do not deliberately commission a new model run for them.

Request review with five facts: PR and head, changes since the previous verdict, checks/evidence links, next action and owner, and any unresolved decision. Do not paste the ticket or repeat the release policy. Reuse one review issue for the same PR across changed heads. Reviewer inspects the changed diff plus affected tests, security and rendered behavior, expanding review where necessary; the new verdict must cover the current head. Prior evidence is reusable only where still applicable.

## Resume from a checkpoint
Maintain one short current checkpoint in the description: current PR/head or deployed commit, last verified result, outstanding action and owner. Read relevant evidence behind that checkpoint, not every historical thread. A changed head, deployment or acceptance criterion can invalidate evidence; a new agent run by itself does not.

On child-review completion, the platform already wakes the parent assignee. Lead must not mention or rerun the same Engineer while that run is queued, dispatched or running. Check actual active runs once. For a new handoff thread, an already_active refusal does not deliver the comment to that run (supplements in the existing thread retain normal routing): leave it recorded, do not retry or switch threads to evade the guard. A genuinely new requirement needs an explicit later handoff after current work finishes, or a human interruption decision.

After a clean fast-lane review and green checks, Engineer merges with the reviewed-head guard and verifies the automatic deployment. No second Lead acceptance. Pending deployment is a checkpoint with its workflow/run link, not another review stage. Stop and let the existing monitor or supported completion event resume verification; do not invent a watcher, poll indefinitely or promise an unconfigured wakeup. Finish the parent when acceptance is met; use human review only when specifically required.

## Clear the actual blocker
Record each independent blocker with evidence, owner and clearing condition. Provider quota recovery does not establish browser capability, permission, review approval or deployment success. Clear only the condition whose evidence changed. Verify capability in the actual worker environment; success on another host or shell is not proof. Keep detailed logs in artifacts and share the first useful error, not an environment inventory.

## Example
PR #199 at 027bc51 is ready for re-review. Since the previous verdict, only the CI route-list conflict changed; both routes remain. Checks and refreshed Medtech screenshots pass [links]. Reviewer: inspect the change and record a verdict for this head. No scope decision is pending.
