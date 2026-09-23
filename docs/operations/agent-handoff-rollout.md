# Agent handoff rollout — 23 September 2026

The duplicate handoff and completion-instruction fix is live on CoderPush Multica.
[PR #5](https://github.com/CoderPush/multica/pull/5) is merged into main.
Production uses its narrow backport on `hotfix/v0.4.43-agent-handoffs`, commit
`013385718f731be728fd7b2c0d09828edb868009`, to avoid unrelated main migrations.

## Production receipt

- Cutover: 23 September 2026, 04:49:52 UTC (11:49 Vietnam).
- Backend image: `multica-backend:handoff-013385718`.
- Image ID: `sha256:cc8ab2cbe3d18c367a63311c1251e6a66fa35d4b5032ba150a87dd7d1c7f08fa`.
- Base: `ghcr.io/multica-ai/multica-backend@sha256:5921256dff4d94b2ee60679534d24d62df82ac305eee13440f5aceaf91a3a769`.
- Only `/app/server` was replaced with a Linux AMD64, CGO-disabled Go build from
  the backport commit. The original entrypoint, migration binary, migrations and
  CLI remain intact. The image is local to the production host; it is not in GHCR.
- Frontend remains upstream v0.4.43. Schema remains
  `467_autopilot_trigger_creator_from_autopilot`.
- Backup: `/opt/multica/backups/database-20260923T044716Z.dump`; archive listing
  validated with `pg_restore --list`. This was not a restore rehearsal.
- Original Compose: `/opt/multica/releases/013385718/compose.before.yml`.
- Build context: `/opt/multica/releases/013385718/`.

The backend image field in `/opt/multica/compose.yml` is pinned directly to the
hotfix image. Existing environment values no longer select the backend image;
future release tooling must update this field deliberately. Other services,
secrets, ports and volumes were preserved. The backend was recreated only after
queued/dispatched/running tasks drained.

## Verification and boundary

The production-baseline handler suite and focused race tests passed. Independent
local review and GitHub Amazon Q review found no blocking defects. The main
baseline's full handler suite has an unchanged clock-skew timestamp-boundary
failure; do not report it as fully green.

Public health reports the backport commit. Readiness reports database and
migrations OK. Hogan Engineer and Reviewer runtimes reconnected; authenticated
CLI reading of CLP-28 succeeded and returned `done`. Ordinary task start/progress
requests returned HTTP 200 after cutover. No inference run was started for this
rollout. These checks do not yet demonstrate lower token usage.

The guard refuses a different agent's separate-thread mention of an already
active direct assignee with `blocked/already_active`. The saved comment is not
silently replayed. Human follow-ups, same-thread supplements, self-continuations
and other specialists retain routing. This is best-effort admission control,
not atomic deduplication of simultaneous requests.

The live `web-delivery` skill and all three reference hashes match the merged
[source](skills/web-delivery/SKILL.md). It is assigned to Web Lead, Engineer,
Reviewer, Triage and SEO. Normal-run skill loading still needs observation.

## Rollback

First check and drain active work. Preserve any later configuration edits before
restoring the saved Compose file. On the production host, restore the original
backend image field from `compose.before.yml`, then run:

```sh
sudo docker compose --project-directory /opt/multica -f /opt/multica/compose.yml config --quiet
sudo docker compose --project-directory /opt/multica -f /opt/multica/compose.yml up -d --no-deps backend
curl -fsS https://multica.coderbase.dev/health
curl -fsS https://multica.coderbase.dev/readyz
```

The original commit is `2ae2dbbb8f9ed9ffe1739ecf5abfe31a940ee50c`. No database
restore is needed for this binary-only rollback; it would discard later writes.

## Follow-through

A bounded daily Codex heartbeat, `verify-multica-handoff-savings`, samples at
noon Vietnam time. It checks at most three changed deliveries, compares lead
runs, tokens, duplicate assignee runs and comment volume with workload context,
and pauses after three completed deliveries or three daily checks. It stays
quiet unless a material finding or action is needed. Existing Multica monitors
remain in place.

Operator baseline, read-only SQL and cutover receipt are under
`~/.codex/agent-handoff-rollout/`. They hold metadata, not credentials
or raw issue/comment text in the baseline. Do not commit private snapshots.
