# CoderPush runtime and Web Lead delivery

These operator-managed tools target the existing CoderPush Web installation.
They do not upgrade Multica, change its database schema, or grant an agent bot
credentials. They are intentionally limited to the workspace, project, Lead,
Lark app and chat constants in the source.

## Hogan role launcher

`codex-role.py` is the existing Engineer/Reviewer isolation wrapper with task-home
support. Keep symlinks named `codex-engineer` and `codex-reviewer`. The daemon's
per-task CODEX_HOME must be the `codex-home` sibling of its workdir. Only the
assigned role's auth is linked there. Preserve daemon-generated skills/config,
scoped GitHub environment and session logs. The Singapore `codex-worker.py`
preserves the same task home inside its existing bubblewrap boundary.

Pinned browser dependency: Playwright 1.60.0 installed outside repositories at
`/Users/webworker/tools/browser/node_modules/playwright`; browsers are installed
under `/Users/webworker/browsers`. Both roles must pass a real daemon-dispatched
browser launch, 1440px/390px screenshots, task skill read and negative credential
boundary checks. SSH success alone does not certify a launchd task.

## Lark outbox

Lead reads the originating conversation when available and queues one concise
milestone through `web-lead-notify CLP-48 --reply-to om_... --milestone recovered
--body-file reply.md`. Only Lead's task identity can use the helper. It writes an
ordinary issue comment carrying a versioned JSON request; **queued is not sent**.
Keep the originating Lark message ID in issue metadata for scheduled follow-ups.

The server's root-owned delivery service reads only marked Lead-authored comments
in CoderPush Web. It validates age (30 minutes), current issue status and payload.
Before sending it verifies the origin against the immutable inbound delivery
record for this Lead installation and Agentic Marketing. It does not request
broader Lark message-read permission; Lark validates reply availability at send time. It replies as the existing Web Lead app in that thread, using the
existing encrypted installation secret only in process memory on the server.
No credentials enter an agent home; no integration/database rows are written.
Multica remains the only inbound websocket consumer.

SQLite receipts and deterministic Lark idempotency keys prevent duplicate sends.
A timeout or interrupted send is **uncertain** and never automatically retried.
The operator must inspect the actual Lark thread before recovery. Duplicate new
requests are suppressed. Only the latest request per issue is eligible; later
changes supersede older unattempted requests. Pending receipts recover directly
from SQLite even outside the query window. An uncertain receipt holds newer sends
for that issue until operator reconciliation. Receipts are also written to the existing
`lark_delivery` issue metadata key through the authenticated Multica CLI. Failed
receipt publication retries without resending. Read the receipt before claiming
delivery. This timer only delivers requests; it does not select or dispatch work.

### Install on the existing Singapore host

Run tests first: `python3 -m unittest discover -s deploy/coderpush -p 'test_*.py'`.
The host already supplies Python cryptography and the authenticated operator CLI
profile under OS user `multica-worker`. Keep that profile and installation secrets
in place. Do not copy them to another host.

Install the sender under `/usr/local/lib/multica-web-lead-delivery/` owned by root;
install the unit/timer under `/etc/systemd/system/`. Set `ACTIVATED_AT` to the
actual activation UTC timestamp in root-only `/etc/multica-web-lead-delivery.env`.
The timestamp prevents historical comments becoming deliveries. Install the queue
helper in the Lead's existing executable path. The root service needs access to
Docker for read-only SQL and the existing encrypted credential, and uses
`runuser` solely for the supported Multica metadata write.

Run `--dry-run --since <activation>` before enabling the timer. Verify one actual
Lead-origin request, its Lark message, receipt and a duplicate replay. Stop with
`systemctl disable --now multica-web-lead-delivery.timer`; retain SQLite receipts
under `/var/lib/multica-web-lead-delivery` to prevent future duplicate sends.
Do not erase uncertain receipts or disable the native Lark connector.

### Boundaries and remaining product work

The sender is an operator integration for this deployment, not a general Multica
outbound API. Adding projects/chats/agents is a separate authorization and review.
No human mentions, images, arbitrary webhooks or arbitrary API endpoints are
supported. Queue content is authored by the authorized Lead, not inferred from
untrusted issue prose. It must not contain credentials or personal data.

For comment-triggered runs, pass `--parent <trigger-comment-id>` to
`web-lead-notify`. This is the Multica comment ID, distinct from the Lark
`--reply-to` message ID. The platform still enforces task-scoped write authority;
the helper does not infer a parent or retry a failed queue write.

## Verification record (21 September 2026)

- Twelve sender tests pass, including interrupted sends, receipt-only retries,
  malformed-origin isolation, copied inbound provenance and superseded receipts.
- Independent bounded review found and corrected two recovery defects and one
  copied-provenance defect. Full CE orchestration was not executed.
- Live Web Lead request `01a0c47c-7693-73f5-94c8-9d8d7555a943` delivered in the
  original Agentic Marketing thread as message `om_x100b6420230db4a8e1886f595d30eab`.
  The operator independently read that message through the user Lark CLI.
- Task-local skill access and session recording now work. Actual Hogan daemon
  browser validation remains blocked by Chromium SIGILL; the same Codex CLI,
  workspace and browser succeed through SSH. PR194 remains held. A SessionCreate experiment was rejected by launchd and is excluded from
  these changes; restoration uses the original service configuration.
