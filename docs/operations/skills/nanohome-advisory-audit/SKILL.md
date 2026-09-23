---
name: nanohome-advisory-audit
description: Audits NanoHome agent configuration and delivery outcomes without taking over execution. Use when Mika runs the existing daily NanoHome setup audit or evaluates an exceptional escalation.
---
# NanoHome advisory audit

Mika owns workspace priorities and exceptional escalation. NanoHome Lead owns delivery. Stay outside the squad and do not dispatch workers, implement, change settings/status, merge, deploy, acquire credentials or introduce an approval gate. Load the assigned nanohome-delivery skill to inspect its contract; its execution permissions do not transfer to this advisory role.

Use the existing daily audit only. Read current configuration, recent bounded runs, board summaries and linked exact-head/release evidence. Compare with the prior audit. Inspect at most five changed/stalled items, expanding only evidence needed for a finding; do not scan the entire backlog or duplicate Lead's storefront check.

Check explicit model/effort/service settings and actual runtime failures; saved settings do not prove successful model execution. Check the single Lead/Lark binding, one coordinator, two delivery pipelines, clear stage/next-owner handoffs, skill assignments, duplicate runs/review issues, idle capacity despite ready work, stale blockers and missing review/release follow-through. Measure verified outcomes, not scan count.

Return at most three new evidence-backed findings, each with impact, recommended owner and next action. Separate observed facts from recommendations. An unchanged unresolved recommendation is not new information. Keep no-change results in run history and stay quiet.

Record a meaningful finding on NANO-2 for Lead evaluation only under the existing audit authorization; avoid a notifying mention unless a new actionable handoff is needed and the Lead has no active/queued run. Escalate only new material failure, risk or an actual Harley decision, with prepared evidence and a recommendation. Link real Multica issue/PR URLs. No polling or waiting.

Example: “QA retains Sol/medium as configured. Two review issues target the same PR/head [links]; Lead should consolidate future review on the existing child. No new reviewer run is needed.”
