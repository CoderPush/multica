# Hark Lark gateway

Run the existing EO Vietnam Hark Lark bot through a Multica chat agent without
requiring Lark senders to have Multica accounts. This is a small owner-operated
gateway, not Multica's native member-only channel installation.

The service uses the official Lark persistent-connection SDK and Multica's normal
chat API. It is fixed to one workspace and one agent. Messages retain their real
Lark sender ID in the prompt; the Multica session belongs to the gateway operator,
not an impersonated sender. No Multica membership or identity bindings are created.

## Scope

- Only configured groups with explicit bot mentions and explicitly permitted DMs.
- Text only. Media receives an explanation instead of being downloaded.
- Chat first. PR commands receive a clear hold and never invoke a model.
- Configure the agent with shell, browser, apps, plugins and subagents disabled,
  no MCP servers, read-only sandbox and no approval escalation. Agent instructions
  must prohibit GitHub, production and workspace operations. This gateway is not
  a substitute for runtime isolation when execution tools are added later.
- Each group thread and sender has a separate Multica chat. DMs are per sender.
- One process and one model request at a time. The durable SQLite ledger survives
  restarts and deduplicates incoming events. Events older than five minutes are
  ignored, so this is not an offline message backfill service.
- An uncertain Multica submission is reconciled by its unique message marker;
  it is never blindly re-submitted. An uncertain Lark reply reuses its UUID for
  less than 50 minutes, then requires operator reconciliation. Raw transport
  errors and failed-runtime diagnostics never go into Lark replies or logs.

## Configuration and run

Requires Node 24+. Run `npm ci --ignore-scripts` in this directory.
Store configuration outside the checkout, mode `0600`, in a directory mode `0700`:

```json
{
  "serverURL": "https://multica.coderbase.dev",
  "workspaceID": "workspace UUID",
  "agentID": "agent UUID",
  "multicaConfig": "/private/path/to/operator/multica/config.json",
  "appID": "Lark app ID",
  "appSecret": "Lark app secret",
  "botID": "ou_bot",
  "groups": ["oc_approved_group"],
  "dmUsers": ["ou_approved_sender"],
  "stateDir": "/private/path/to/gateway/state"
}
```

The operator token stays on Hogan and is never passed to the model. It can carry
broader Multica authority than the fixed gateway routes; protect the host/config
and use a dedicated scoped service credential if the platform adds one. The
gateway reads the existing CLI token each request so authorized token renewal is
picked up automatically. It refuses a different API destination and redirects.

Start with `HARK_GATEWAY_CONFIG=/private/path/config.json node main.mjs`.
The process logs event IDs and task/reply receipts, not content or credentials.
Run `npm test` for input policy, isolation, deduplication, interrupted submission,
failure redaction, reply fencing and PR-command hold tests.

## Cutover and rollback

1. Back up the existing Hark agent settings and OpenClaw account configuration.
2. Verify the restricted Multica agent completes a new real chat on Hogan.
3. Check that the old Hark request ledger has no active operation. Stop only the
   Hark background/followup services. Preserve their ledgers and release gates.
4. Disable only OpenClaw's `hark-eo` account through `config.get` and `config.patch`
   with the current base hash. Never edit OpenClaw's JSON file directly.
5. Start this gateway. Never have both persistent clients consuming the same app:
   Lark distributes events between connections rather than broadcasting them.
6. Send a real Lark test and verify one reply in the original conversation, its
   matching Multica task, a follow-up, and the explicit PR-command hold.

Rollback: stop this gateway first, restore the saved OpenClaw `hark-eo` enabled
value through the same supported patch path, and restore the prior Hark services.
Keep the gateway ledger for reconciliation. Do not replay uncertain model requests
or old PR operations. Other OpenClaw bots and Multica workspaces remain unchanged.

This source alone is not evidence of deployment or a successful Lark round trip.

## Legacy production observer (disabled)

Set `releaseObserver: true` in the private configuration to enable a five-minute
observer in the existing gateway process. It uses Hogan's existing `gh` login
with fixed GET requests to `EO-Vietnam/eo-vietnam`. Vercel's production deployment
statuses arrive through GitHub; no Vercel token or browser session is required.
The installed `gh` executable is `/Users/hogan/.local/bin/gh`.

The first run establishes a quiet baseline for `/`, `/blog`, and `/events`.
Subsequent deployments also check up to 12 static public page routes in the
GitHub comparison from the previously observed deployment. New article routes
are checked for a direct link in `/blog`. Dynamic routes, content-only data
changes without a page file, visual layout, images, logged-in flows, payments,
and database migrations are outside this pilot's verification coverage.

Confirmed HTTP failures (two consecutive observations), Vercel deployment failures,
and recoveries go only to the approved `eovietnam.org alerts` group. Unresolved
pages stay in the check set across unrelated deployments before recovery is claimed.
Healthy deployments stay quiet because the repository already posts those.
The durable outbox retries with one UUID for under 50 minutes after its first
send attempt and rotates pending rows fairly; older uncertain
sends are held for operator reconciliation. No GitHub or production writes occur.
The latest timestamped evidence is captured when a queued Lark chat is submitted;
Hark must identify snapshots older than 15 minutes as stale. Read failures leave
the old timestamp intact and record a sanitized operator log. There is no external
uptime monitor for Hogan or the gateway process in this pilot.

Run `node main.mjs --check-observer` with the normal private config and the live
gateway stopped to collect a baseline without sending the outbox. This mode
updates observer state and may queue a problem notice; it does not send it.
Disable the observer flag and restart the gateway to roll back just the observer.
Preserve `observer.sqlite` and reconcile held outbox entries before replaying.

## Hourly Multica Autopilot

The current rollout keeps `releaseObserver: false`. A native Multica **Run only**
Autopilot assigns Hark Release Watch on Hogan once per hour. It does not create
issues or invite teammates. The separate chat agent retains no execution tools.

`release-mcp.mjs` exposes three fixed-scope tools: discover newly merged main PRs,
inspect queued release evidence, and finish a check with an optional message to
the fixed alerts group. The server owns GitHub GET requests, bounded public HTML
reads, and Lark credentials. No arbitrary command, repository, URL or recipient
is accepted from the model. Keep shell, browser, apps and other MCP servers off.
The Codex tool host must be enabled for MCP calls; only these three tools should
be preauthorized for unattended execution. Test the effective runtime settings,
not just the saved agent configuration.

The private `releases.sqlite` database establishes a start-time baseline, retains
unfinished checks, and avoids replaying completed PRs. Discovery overlaps one day
for GitHub indexing and rejects a backlog over 100 results. Each run handles up
to five least-recently-inspected items. It inspects at most 100 changed files;
truncation is reported, never treated as a complete code audit. Deployment evidence
must include the merge commit before public checks can complete the item.

The runbook requests alerts only for concrete findings, material clarifications,
stalled deployments and recoveries. Healthy releases stay quiet. Reports include
their verification time and coverage limits. Delivery uses a durable UUID; an
uncertain attempt older than 50 minutes requires operator reconciliation. This
can leave a queued item pending rather than risking a duplicate message.

Vercel production status comes through GitHub. Vercel runtime logs, visual or
interactive flows, database migrations and payment verification are not covered.
Hogan must be online and its GitHub/Codex authentication usable. A completed
Multica task is not itself proof that tools ran; inspect its result and the stored
evidence/receipt. Tool failures retain pending work for a later run.

To commission, run `release-mcp.mjs --baseline` with the private config, optionally
queue one explicitly approved historical PR with `--seed-test NUMBER`, then run
the Autopilot manually and verify its actual Lark receipt. After that, enable its
hourly schedule. Pause the Autopilot to stop release checks; the chat gateway stays
running. Preserve the ledger when changing schedules or restarting Hogan.
