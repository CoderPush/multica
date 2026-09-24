# CoderPush Multica production

This fork, `CoderPush/multica`, is the working repository for Multica enhancements
and production operations. Open enhancement PRs against this fork's `main`, not
upstream. Production environment changes are performed on the host; record their
non-secret intent and verification here. Never commit credentials or database dumps.

For the new team workspace, squad, runtime setup and remaining onboarding steps,
see [CoderFactory team setup](coderfactory.md).

## Current release — 24 September 2026

Production runs CoderPush main commit `355006fc62cc379eabeb61b2a9ee50e2707e2dd7`,
which merges upstream `e909e9c89d524cd54c1d5f4fa5963882093efdc9` and preserves
our handoff fixes. Backend and frontend are pinned to verified local image IDs;
schema is 547. Cutover completed at 08:13:28 UTC. Health, readiness, signed-in UI,
all 79 previously online runtime registrations and Lark websocket reconnection
were verified. Existing signup configuration and secrets were preserved.

See [the upgrade receipt](upstream-upgrade-20260924.md) for image identities,
CI, migration/restore rehearsal, final backups, and rollback requirements.
The original local pilot containers and volumes were removed. The development
database is retained but stopped; it is only needed for fork development/tests.

The base `/opt/multica/compose.yml` now pins the new images and disables pulling;
the matching `/opt/multica/coderpush-images.yml` overlay is retained. Base-only
Compose operations have the same effective configuration. GHCR packages remain
private. Use the manual **Export CoderPush release images** workflow when the
host lacks registry credentials; validate archive checksum, original registry
digests and imported OCI image identity as the receipt describes. Never copy
personal or runtime GitHub tokens to the host. `DO_NOT_TRACK=1` disables the new
upstream telemetry sender.

## Historical inventory — 18 September 2026

Production is **AWS Lightsail Singapore**, not Hetzner. Hetzner was evaluated
before the Lightsail deployment on 15 September. DNS and SSH verified the current
host; the instance size and snapshot schedule below come from the deployment
handover and have not been rechecked in AWS this session.

| Item | Value |
| --- | --- |
| Application | https://multica.coderbase.dev |
| Fork | https://github.com/CoderPush/multica, default branch `main` |
| Upstream | https://github.com/multica-ai/multica |
| Host | `52.76.32.3`; SSH user `ubuntu` |
| Recorded instance | `multica-coderpush`, ap-southeast-1a, Ubuntu 24.04, 8 GB RAM, 2 vCPU, 160 GB disk |
| SSH identity on Harley's Mac | `~/.ssh/LightsailDefaultKey-ap-southeast-1.pem` |
| Application config | `/opt/multica/compose.yml`, `/opt/multica/.env` (root-only) |
| Running images | `ghcr.io/multica-ai/multica-backend:v0.4.43`, `ghcr.io/multica-ai/multica-web:v0.4.43` |
| Running backend commit | `2ae2dbbb8f9ed9ffe1739ecf5abfe31a940ee50c` |
| Database | `pgvector/pgvector:pg17`, migration `467_autopilot_trigger_creator_from_autopilot` |
| Persistent volumes | `multica_pgdata`, `multica_backend_uploads` |
| Reverse proxy | Caddy; `/etc/caddy/Caddyfile`; HTTPS to loopback ports 8188 and 3318 |
| Caddy backend routes | `/health`, `/readyz`, `/ws`, `/ws/*`, `/api/daemon/ws`; other requests go to frontend |
| Worker | `multica-worker.service`, OS user `multica-worker`, 5 GiB memory limit |
| Worker root | `/srv/multica-worker`; role homes under `runtime/` |
| Worker CLI profile | `/home/multica-worker/.multica/profiles/server/` |
| Database backups | `/opt/multica/backups`, `multica-backup.timer`, daily 23:55 UTC |
| Token renewal | `multica-github-tokens.timer` and `multica-internal-github-tokens.timer` |

Readiness reported database and migrations OK. Caddy, Docker, worker and backup
timer were active. The latest database dump was 17 September at 23:55 UTC (about
1.2 MB). This is service verification, not a fresh login, Lark, or agent task test.

Current release image digests, retained for identification:

```text
backend: ghcr.io/multica-ai/multica-backend@sha256:5921256dff4d94b2ee60679534d24d62df82ac305eee13440f5aceaf91a3a769
frontend: ghcr.io/multica-ai/multica-web@sha256:fc937fbbf8e5a87d166e5e1e1420acec38aea29cec93570006c2d528063dbd45
```

## Read and change production settings

```sh
ssh -i ~/.ssh/LightsailDefaultKey-ap-southeast-1.pem ubuntu@52.76.32.3
sudo docker compose --project-directory /opt/multica -f /opt/multica/compose.yml ps
sudo systemctl is-active caddy docker multica-worker multica-backup.timer
curl -fsS https://multica.coderbase.dev/readyz
curl -fsS https://multica.coderbase.dev/health
```

Edit only the needed keys in `/opt/multica/.env`, after making a root-only dated
backup. Preserve JWT and integration encryption keys: replacing them can invalidate
sessions or make stored integration credentials unreadable. Validate without
printing interpolated secrets:

```sh
sudo docker compose --project-directory /opt/multica -f /opt/multica/compose.yml config --quiet
sudo docker compose --project-directory /opt/multica -f /opt/multica/compose.yml up -d --no-deps backend
```

Compose `restart` alone does not apply changed environment values. Recreate only
the affected service, then verify readiness and the feature changed. Check active
agent work before interrupting backend or worker connections. If a setting is
missing from the installed Compose environment mapping, update that mapping too.
Keep root frontend and backend ports on loopback. Never run `down -v` here.

Do not print `.env`, full Docker inspect/config output, private keys, runtime auth
files or dumps. To inspect environment key names, parse Docker's JSON environment
array and split each element once at `=`; line-based filtering leaks multiline PEM
values. Credential values remain host-side. Do not copy the historical deployment
folder wholesale: it contains secrets and database exports.

The signup allowlist is separate from workspace invitations and agent invocation
access. A workspace invitation alone does not bypass instance signup policy.
Check current values on the host before changing access; older allowlists are stale.

For CoderPush team onboarding, keep `ALLOWED_EMAIL_DOMAINS=coderpush.com` in
`/opt/multica/.env` and mapped into the backend Compose environment. Use the bare
domain, without `@` or a wildcard. Preserve `ALLOWED_EMAILS` for approved individual
exceptions outside that domain. Do not add each new CoderPush colleague to the
individual list: the domain policy covers them. This enables account signup;
workspace invitations and membership checks still apply. Recreate the backend
after policy changes and verify its effective environment, not just the file.

## Deployment from this fork's main

**Activated on 24 September 2026.** The first fork release and restore rehearsal
are complete; see the current release and receipt above. There is no automatic
main-to-server rollout. Each future release still needs matched image builds,
CI and migration review. The first upgrade applied 77 migrations from schema
467 through 547. Migration 468 deletes obsolete link rows and drops columns;
an image-only rollback to v0.4.43 is not sufficient.

The fork workflow `.github/workflows/coderpush-images.yml` is manually dispatched
on `main` and publishes Linux AMD64 backend/frontend images tagged with the full
commit SHA. It uses the workflow's package token and needs no production SSH key.
It does not publish a moving `latest` tag or deploy anything. Both build jobs must
succeed for the same SHA; a partial publication is not a release. Existing CI must
also pass for that SHA. The build and export workflows are committed and available on `main`.

1. Review the intended `main` commit, changes since the running version, migration
   compatibility, and existing CI results. Build both images using **CoderPush main
   images**, then record their registry digests. If the packages are private, set
   up host pull access with a read-only package credential; never reuse agent GitHub
   tokens or add a broad personal token to application configuration.
2. Rehearse migrations and application startup using a protected backup restored
   to an isolated database, with Lark/Slack connectors, webhooks, outbound email,
   schedulers and workers disabled. Do not start a second production connector.
   Check migration success and compatibility with the installed desktop and daemon.
3. Before cutover, drain active work and stop the worker and backend. Take a fresh
   database dump using `/usr/local/sbin/multica-backup`, check it with `pg_restore
   --list`, and retain uploads, Compose, environment and worker configuration in
   protected backup storage. The daily script backs up **only PostgreSQL**. Snapshot
   scheduling was recorded previously but not verified in AWS today. A restore
   rehearsal is still required; file presence is not restore proof.
4. Put `deploy/coderpush/images.yml` on the host as
   `/opt/multica/coderpush-images.yml`. Set `CODERPUSH_BACKEND_IMAGE` and
   `CODERPUSH_WEB_IMAGE` in the protected host `.env` to the reviewed digest
   references. Keep the existing base Compose file, project name, ports and volumes.
   Use both files for every subsequent Compose operation:

   ```sh
   sudo docker compose --project-directory /opt/multica \
     -f /opt/multica/compose.yml -f /opt/multica/coderpush-images.yml config --quiet
   sudo docker compose --project-directory /opt/multica \
     -f /opt/multica/compose.yml -f /opt/multica/coderpush-images.yml pull backend frontend
   sudo docker compose --project-directory /opt/multica \
     -f /opt/multica/compose.yml -f /opt/multica/coderpush-images.yml up -d --no-deps backend frontend
   ```

5. The backend entrypoint runs migrations before serving. Verify readiness,
   `/health` commit, login, existing workspace/project data, desktop connections,
   and runtime reconnects. Restart the worker when the API is healthy. Run an
   explicitly authorized bounded agent/Lark test before declaring full acceptance.
   Record the deployed SHA, both digests, backup identifier and results here.
6. If the upgraded schema is incompatible with the previous application, keep
   writers stopped and restore the pre-upgrade database and matching application
   images/configuration together. Account for any writes after cutover; do not
   silently discard them. Do not assume down migrations recover deleted data.

Build off the production host where practical: its worker shares the same CPU and
memory. Environment changes can remain independent of application releases.
Automatic deployment on every push is a separate decision after the first
controlled upgrade and recovery rehearsal.

## Runtime and integration context

The following context was recovered from earlier tasks. It is dated evidence, not
a claim that each integration was retested on 18 September.

- The original local stack at `/Users/qron/Projects/coderpush-multica-selfhost`
  migrated to Singapore on 15 September. The local backend/frontend and old daemon
  were stopped; avoid reviving the old Lark websocket consumer.
- The historical deployment directory is
  `/Users/qron/Projects/coderpush-multica-deployment`. Its README and bootstrap
  artifacts are useful references but include stale instructions and private files.
- Worker commands use bubblewrap with separate role homes. The AppArmor profile
  `/etc/apparmor.d/multica-bwrap` permits its user namespaces without disabling
  Ubuntu's global restriction. Preserve the resolver mount when changing sandboxing.
- Web Lead, Web Engineer, Code Reviewer and SEO Analyst form the web squad in
  workspace slug `coderpush-lark-pilot`. Web Lead coordinates; Engineer implements;
  Reviewer reviews the PR head; SEO reads reporting data. Tokens are role-scoped
  and renewed every 30 minutes. Running tasks can retain expired launch-time tokens.
- On 18 September all four web agents were changed to **Entire workspace** invocation
  access. This supersedes the old owner-only pilot. Lark identity linking remains a
  separate concern; do not infer another person's access from Harley's successful run.
- Web Lead can coordinate release after recorded authorized human approval; Engineer
  rechecks the approved PR commit and checks, merges, verifies production, and returns
  evidence. Anh may approve simple low-risk website changes after technical checks;
  consequential technical decisions go to Harley. These rules belong to the web squad
  and do not grant blanket merge/deploy authority for this Multica fork.
- Lark app version 0.1.2 was released on 18 September with display name **Web Lead**
  and description “Connects the Web Factory squad to Lark. Bring it website and
  marketing requests.” Preserve the existing international Lark installation.
  The proposed separate Lark bridge was not delivered in the recovered history.
- GitHub server integration and execution credentials are separate. The original
  `CoderPush/web` installation and sandbox grants were limited by role. Live host now
  also has a CoderInternals token timer; inspect that setup before assuming the old
  single-repository scope still describes the whole server.
- Resend was configured for login mail from `multica@coderbase.dev`; delivery was
  verified on 15 September. Vercel MCP was authorized for Web Lead with broad OAuth
  permission: project context and prompt rules do not make that credential read-only.
- SEO's separate Google service account has GA4 Viewer and Search Console Restricted
  access; CLP-5 verified real reports on 15 September. Do not distribute its key to
  other runtime homes. LinkedIn was not connected in that handover.
- “Online” only proves runtime connectivity. A later NanoHome setup task observed
  Codex 401 authentication failure on Singapore. Model auth belongs to the runtime
  OS user/home; refreshing the Mac's login does not repair server credentials.

## Source tasks and outstanding work

- `01a0a444-2fbe-7d10-ab7d-661b246f28cc` — **Update Multica Desktop server**:
  local setup, hosting decision, migration, sandboxing, backup and integrations.
- `01a0b28f-9da5-7b60-a6c6-c45d52dac458` — **Set up Multica and Lark**:
  latest web-agent access, approval handoff and Lark rename.
- `01a0adbe-0b3c-7cf0-bdbe-371a8ae36194` — **Investigate multica setup failure**:
  Singapore model authentication diagnosis.

Open items: backup retention/off-host verification; reconcile newer CoderInternals/NanoHome runtime
configuration before changing shared services. Keep live runtime edits and repository
delivery status separate: a host change does not mean a PR was committed or merged.

## Web runtime access — verified 20 September 2026

For CLP-15, the daemon's repository checkout had no Git credential even though
agent wrappers supplied role tokens. The daemon now uses
`/usr/local/bin/multica-web-git-credential` through the worker user's Git config.
The helper reads the renewed Web Lead token on each invocation and only answers
HTTPS credential requests for `github.com/CoderPush/web(.git)`. It supplies no
credential for other hosts or repositories. It uses the existing contents-read
grant; Engineer retains its separate contents-write token inside its runtime.
Web Lead subsequently verified `multica repo checkout` in an actual agent run.

Harley approved Actions read-only for the existing CoderPush Multica GitHub App
installation. `/usr/local/sbin/multica-refresh-github-tokens` now requests that
permission for the three web role tokens, still restricted to CoderPush/web.
All three read the failed Actions run; Engineer's token also downloaded the log
archive. The installed `gh run view --log-failed` returned empty output with exit
0 for that run; the Actions run logs API returned the actual failure. Do not treat
empty CLI output as evidence of a clean run.

Direct desktop/API chat messages do not create a Lark channel delivery record,
even in an existing channel-origin chat. To get a native Web Lead reply into its
original Lark topic, mention Web Lead in that topic. The CLP-15 topic trigger was
verified to create a delivery record for the correct topic. Do not inject database
delivery rows or give agents the server's integration secrets to work around this.

## Worker capacity — verified 20 September 2026

At Harley's request, the shared Singapore worker now starts with
`--max-concurrent-tasks 2` in `/etc/systemd/system/multica-worker.service`.
The service was restarted after its running/dispatched/preparing queue was empty;
the replacement process and daemon status confirmed capacity 2 and healthy runtime
registration. The 5 GiB memory cap and TasksMax 512 remain unchanged. These are two
shared task slots across the daemon's workspaces, not a reserved coordinator slot.
The prior unit is backed up on the host as
`multica-worker.service.backup-20260920T071152Z`.

The Web delivery monitor and backlog autopilot completed its first hourly cycle:
it verified PR #184 in production, reconciled stale backlog items, and dispatched
CLP-24, which produced PR #185. This verifies one monitoring-to-implementation
cycle; it does not yet demonstrate sustained two-task load or Anh-originated
runtime authorization.

## Internal Apps Dependabot access — verified 20 September 2026

Harley approved Dependabot alerts read-only for the existing CoderPush Multica
GitHub App and accepted its installation update. The installation still selects
`CoderPush/internal` and `CoderPush/web`; no repositories were added. Installation
permissions are shared across those selected repositories, while runtime tokens
remain narrower.

`/usr/local/sbin/multica-refresh-internal-github-tokens` now explicitly requests
`vulnerability_alerts=read` for Internal Apps Lead, Engineer and Reviewer, each
restricted to repository ID `1017468524` (`CoderPush/internal`). Other permission
fields and the web token generator were unchanged. The prior generator is saved
as `.backup-20260920T081928Z` alongside the script. All three refreshed tokens
returned only CoderPush/internal from the installation-repositories endpoint and
read all 150 open critical/high alerts (23 critical, 127 high) through cursor
pagination. The renewal timer remains active. Existing agent processes may retain
launch-time tokens until their next run; no worker restart was performed.

Harley's Codex task also maintains hourly sanitized alert and domain-resolved
Vercel deployment evidence for CLP-17 using existing local authentication. Vercel
credentials were not copied into Multica. Collection does not establish alert
reachability or authorize remediation merges. The host access change is active;
this runbook update remains local and uncommitted.

## Model and monitor usage settings — verified 23 September 2026

Harley requested current models with lower usage for CoderPush, NanoHome and
EO/Hark. Fifteen user-facing Codex agents were updated through the authenticated
Multica CLI and read back from production. Twelve now use `gpt-6-sol` with
`medium` reasoning. Code Reviewer and SEO Analyst use `gpt-6-luna` with `medium`
reasoning; Hark Release Watch uses `gpt-6-luna` with `low` reasoning. All fifteen
explicitly select standard service (`default`). NanoHome's six agents previously
had no model override; Hogan's local default was `gpt-6-astra`, so explicit agent
settings remove that inheritance. CoderPush's Claude-backed Mika, internal builder
agents, CoderFactory and TPS settings were not changed.

Singapore's Codex CLI was upgraded from 0.153.4 to 0.156.0, matching the inspected
Hogan CLI. The complete prior tool installation is retained at
`/opt/multica-agent-tools.backup-model-upgrade-20260923`. The installed CLI's
`model/list` now includes GPT-6 Sol and Luna; Hogan's catalogue also includes both.
The agent settings backup is on Hogan at
`/Users/hogan/.multica/model-settings-backup-20260923T031139Z.json` (mode 0600).
No worker restart or additional model inference test was performed. Settings
apply to subsequent launches; existing runs were not interrupted. Catalogue and
saved-setting checks do not prove every runtime's authentication or task execution.
Internal Apps Lead had an existing provider-auth/access failure that this update
does not establish as resolved.

At approximately 10:05 Vietnam time, the task table showed 41 NanoHome delivery
coordination launches since local midnight, including quota failures. Harley
selected hourly checks from 08:00 through 20:00 Vietnam time. The existing trigger
`ddcf0ed3-5fe1-4e8f-a2cd-b21014476ff9` on autopilot
`9c60c180-4fda-4450-ad48-96d389a9c033` now uses `0 8-20 * * *` with
`Asia/Ho_Chi_Minh`, replacing `*/15 * * * *`. The API returned its next run as
11:00 Vietnam time on 23 September. This reduces scheduled wakeups from 96 to 13
per day (about 86%); it is not a measured token or subscription-usage saving.
The separate daily NanoHome audit and other autopilot schedules are unchanged.

Production settings are active. This appended operations record is local and
uncommitted; it does not constitute a fork application release.

## Web flow pilot — 23 September 2026

Harley approved agent-led readiness and end-to-end delivery for routine CoderPush
Web work within the existing fast lane. This is scoped to project
`1f065765-5630-4e67-9f16-adffaa6b3ce3` and Web Factory squad
`403d083c-e6ca-4afd-83f8-9aeca8fb41a9`. Exception approvals and explicit human
pauses remain binding. It does not extend to Internal Apps, NanoHome, EO or TPS.

This supersedes the Web Lead and Code Reviewer model assignments above. Web Lead
now uses GPT-6 Luna at low effort, with one concurrent run. Web Triage
(`0e3b6e47-602e-4d51-9d56-1c3b9ba6a0f4`) uses GPT-6 Sol at medium effort, with
one concurrent run, on the existing Hogan Reviewer runtime. No credentials were
copied or runtime permissions changed. The new agent is workspace-invocable and
a Web Factory member. Engineer and substantive Code Reviewer use Sol/medium.

The Lead routes clear work directly and calls Triage only for ambiguity or
incomplete briefs. Triage can approve routine scope and dispatch ready work
without another Lead or human approval. Issue descriptions hold the current
outcome, scope/acceptance, release lane, evidence, next owner/action and blocker.
Settled comment decisions are incorporated with source links; history is
preserved. Bookkeeping writes use `--no-start`, without notifying mentions.

Use the existing columns: Backlog parks unselected/unready work; Todo means
ready; In Progress includes implementation, agent review and release verification;
In Review means a named human decision; Blocked names the dependency and owner;
Done requires verified acceptance. Production v0.4.43 dispatches on assignment
outside backlog or promotion out of backlog, not every column transition.
Comments can independently trigger work. These are configured operating rules,
not a new server-enforced readiness validator or universal column-trigger engine.
Check queued/running work before one intentional handoff; do not also mention or
start the same executor. Keep two delivery items active at most. Reuse one review
child per PR rather than creating one for each review attempt.

The existing hourly Web delivery monitor was updated in place to inspect compact
summaries first, expand at most three changed/stalled items, and dispatch at most
one eligible backlog item per pass. It stops on unchanged blockers and does not
poll. The existing daily Board Hygiene sweep now samples at most five changed
web delivery issues and compares lead/triage overhead, duplicate runs/comments,
repeated review tickets and missing next owners. No schedule or recurring job was
added. Findings stay in run history unless a meaningful milestone or human action
requires notification. Instructions do not establish measured savings yet.

Pre-change settings are retained on Hogan in
`/Users/hogan/.multica/web-flow-backup-20260923T032601Z.json` (0600); the new
agent ID and configuration receipt are in
`/Users/hogan/.multica/web-flow-pilot-20260923.json`. Settings were read back.
CLP-65 is the bounded initial triage verification: improve descriptions for
CLP-29, 54, 51, 63 and 64 without changing target status/assignee, starting work,
or posting target comments. Its native dispatch started task
`01a0cc4d-a408-7166-878e-38895d8c6aef`. That first run failed before making
changes: its isolated worker rejected GPT-6 Sol. The Hogan interactive CLI and shared tool installation were different versions.
The shared installation at `/Users/Shared/coderpush-worker-tools/npm` ran
0.153.4, but was not yet confirmed as the worker launch target. That installation was
backed up as `npm.backup-20260923-models` beside it and upgraded to 0.156.0.
One fresh verification run, `01a0cc50-17ff-7f12-8d3a-79f194ef26ae`, was then
queued. Do not infer role compatibility from the interactive user's catalogue.
The fresh run also failed with the same provider model-access rejection. No
further inference retries were dispatched. Singapore Web Lead's separate run
created at 03:12 UTC completed with usage attributed to `gpt-6-sol`; that proves
neither GPT-6 access nor repair for the isolated Hogan reviewer account. CLP-65
has not completed its description cleanup. Temporary compatible models or a
dispatch pause were presented to Harley for a decision.

Harley subsequently chose to keep GPT-6 and repair Hogan directly, preferring it
over Singapore for agent execution. Web Lead reasoning was raised from low to
high; Max is appropriate for selected hard decisions, not an automatic monitoring
default. Triage was briefly rebound to Singapore, but changing runtime cleared
its model override. That verification run (`01a0cc59-0e23-7f32-b306-5eda1471abc0`)
was cancelled; the model was restored explicitly. Triage is now back on the
Hogan Reviewer runtime with `gpt-6-sol`, `medium`, standard service. No successful
triage execution or issue cleanup has been verified yet.

The earlier shared-install upgrade does not prove which executable the protected
Hogan wrapper launches. SSH as `hogan` cannot read `/Users/webworker` without
admin authentication. A read-only diagnostic is staged at
`/Users/hogan/.multica/diagnose-webworker-gpt6.py`; it reports wrapper path
references and model catalogue names without reading credentials. Its output
will be `/Users/hogan/.multica/webworker-gpt6-diagnostic.json`. Admin execution
was requested; no blanket sudo or filesystem permission change was made.

Hogan's 10:40 Vietnam snapshot showed 16 GiB RAM, eight logical CPUs, load about
1.7 and 13 GiB disk available. Existing swap occupancy alone does not establish
current memory pressure. Keep the isolated worker's two-task limit; low free disk
is the observed capacity concern. Do not remove unrelated projects or caches
without a scoped cleanup decision.

The completed privileged diagnostic resolved the worker executable to
`/Users/webworker/tools/npm/lib/node_modules/@openai/codex/bin/codex.js`.
The earlier shared-install update did not change this separate installation.
Engineer and Reviewer model caches dated 21 September lacked GPT-6 Sol/Luna.
A scoped updater is staged at `/Users/hogan/.multica/update-webworker-gpt6.py`: it
checks the resolved path and active worker processes, backs up the Codex package,
installs 0.156.0 as webworker, verifies the CLI version, and renames only the two
model caches for refresh. It preserves credentials, wrappers and service settings.
Execution still requires an administrator password; no repair or successful
GPT-6 inference is claimed until that execution and the bounded CLP-65 run verify.

The administrator ran the scoped updater at 03:51 UTC on 23 September. Its
receipt `/Users/hogan/.multica/webworker-gpt6-update.json` confirms the actual
worker package changed from 0.153.4 to 0.156.0, with package and role model-cache
backups retained. CLP-65 verification run
`01a0cc64-3476-7e35-85fc-63e8c35add5e` started on the Hogan Reviewer runtime
and successfully produced model responses and Multica tool calls, clearing the
prior immediate model-access rejection. Final issue-update acceptance remains
pending until that bounded run finishes and its saved descriptions are checked.

The verification run completed at 03:55 UTC without error; recorded usage names
`gpt-6-sol`. All five saved briefs were inspected. Two remaining description
errors were corrected directly with `--no-start`: CLP-28 plus its review child
counts as one delivery item, and a fast-lane review does not gain an additional
human approval gate merely because its retained status is in_review. CLP-65 was
closed after acceptance. The target issue statuses and assignees were preserved.
This verifies Sol on the actual Hogan Reviewer runtime; it is not a separate
Engineer-role or Luna inference test. No passwordless sudo grant was installed.

## Model and brief refinement — 23 September 2026

All 16 Codex agents in CoderPush, NanoHome and EO have explicit GPT-6 overrides.
Coordination (Web/Internal/NanoHome Leads and NanoHome Mika) and Hark Release
Watch use Luna/high; SEO Analyst uses Luna/max. Engineering, QA, platform,
substantive code review, Hark and Web Triage retain Sol/medium. CoderPush Mika
remains on its separate Claude runtime. TPS and global interactive CLI defaults
were not changed. Settings were verified; this is not a new inference test of
every role. Active runs may retain their launch-time settings.

Workspace skill `web-delivery` (`176b8ca3-b770-4667-8b72-7fdb8fa8989c`) is assigned
to Web Lead, Engineer, Reviewer, Triage and SEO. Source: [SKILL.md](skills/web-delivery/SKILL.md).
It centralizes concise ticket briefs, material-only comments, one intentional
handoff, parent/review capacity counting and fast-lane completion. Browser/CI
and Lark transport details live in on-demand references; duplicated blocks were
removed from Lead, Engineer and Reviewer prompts. Assignments and content were
read back; task-side loading awaits the next ordinary run.

CLP-28's 667-word stale description was replaced with a current delivery brief,
preserving ticket-specific acceptance and links to history. GitHub production
verification run 35816856955 succeeded for 833e253ea1dd0767cb091beb983bd5af5714f5ed.
CLP-28 was closed with --no-start after saved-text verification. No new model run
was started for this bookkeeping change. Original settings/brief snapshots from
this refinement are retained in the operator's /tmp/multica-tune for this session.

### CLP-28 handoff corrections

The live web-delivery skill now includes an Engineer contract for concise PR/head
handoffs, delta review on the same review issue, checkpoint-based resumption,
independent blocker clearing, and pending-deployment follow-through without new
watchers. Read-back verified the content and reference on the existing skill.

Local backend changes replace the child-completion instruction to create another
stage or always enter human review. Completion is now conditional on acceptance
and actual approval requirements. Agent mentions in a separate thread are refused
with the existing already_active outcome when the target is the active direct
assignee. Human follow-ups, same-thread supplements and other specialists retain
their routing. This admission check covers the observed sequential duplicate;
it is not a global atomic deduplication mechanism for simultaneous new threads.
The narrow v0.4.43 backport was deployed at 04:49:52 UTC on 23 September.
See [the rollout receipt](agent-handoff-rollout.md) for commit, checks and rollback.
Schema remains at 467; upgrading to current main still requires the full migration
procedure above.

Verification: the database-backed routing/stage regression tests pass with the
race detector, including human messages, same-thread supplements, specialist
mentions, completed assignees and deliberate self-continuations. The full handler
suite still fails in the unchanged clock-skew fallback test
`TestReportTaskMessagesFallsBackWholeBatchForClockSkew` (database timestamp falls
about a millisecond before the asserted window). The first review caught an
over-broad self-continuation refusal; the guard now applies only to a different
agent coordinating the assignee. No schema or API enum changes were needed.

## CoderPush signup domain — 24 September 2026

At 03:30 UTC, enabled `ALLOWED_EMAIL_DOMAINS=coderpush.com` on the production
backend. The previous domain list was empty, so only seven individually listed
addresses could register. All seven existing `ALLOWED_EMAILS` entries and
`ALLOW_SIGNUP=true` were preserved. Future CoderPush invitees need no individual
server allowlist edit; other domains still require explicit exceptions.

Root-only environment backup:
`/opt/multica/.env.before-coderpush-domain-20260924T032752Z`.
Compose validation passed. Active/queued tasks reached zero before recreating
only the backend, using the same `multica-backend:handoff-013385718` image.
The running environment was read back. Public health reported commit
`013385718f731be728fd7b2c0d09828edb868009`; readiness reported database and
migrations OK; the worker service remained active.

Eight isolated Go policy checks against the signup-gate functions extracted from
that production commit passed: new CoderPush addresses, uppercase domain,
existing NanoHome exceptions, unapproved domains, subdomains and suffix lookalikes.
These were policy checks, not the database-backed handler suite. No login email
was sent or person impersonated; a newly invited person's completed login and
workspace acceptance remain unverified. No application code or image changed.
