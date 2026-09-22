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
