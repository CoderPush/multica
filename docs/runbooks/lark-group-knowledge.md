# Lark group knowledge pilot

Status: implementation only. This change does not deploy, change app permissions or subscriptions, send production messages, or stop the existing laptop monitor.

## Scope and authority

One operator-configured installation, app, workspace, agent, project, owner and group. The existing authenticated Lark persistent connection remains the sole event ingress owner. There is no new webhook, challenge handler or competing consumer. The channel router validates the active installation before durable capture, and acknowledges successful handling only after the event/source/job transaction commits.

`capture` records human messages and uploads and reconciles history while preserving the existing private route. It makes no model calls or candidate writes and sends no knowledge replies. `process` consumes the configured group's messages before the private agent route. A current group member can ask a sourced question without gaining private agent access. DMs and other groups retain existing authorization. Bot messages, forwarded-message expansion and linked-document contents do not enter the source index.

PDF/DOCX text and original bytes are stored separately from private assessments. Candidate updates use the installation owner's workspace and agent permissions. They preserve human description text, comments and existing hiring stages. Identity conflicts quarantine the job. Names are used in candidate titles; authored assessments focus on claim evidence, not hiring decisions. The baseline is Published CTO JD v1 (7 October 2026), encoded in `knowledge_assessment.go`; changing it needs a reviewed reassessment policy, not a silent cache reset.

Group answers select exact quotes from current group sources. They do not read candidate rows, private assessments, private chat history or agent tools. Source access and requester membership are checked before generation and again before delivery. Replies require provider-supplied source links; missing links do not become invented URLs. Linked documents/wiki and live document-change subscriptions are deferred.

## Prerequisites and configuration

Use existing authorized managed hosting, PostgreSQL, object storage, Lark installation credentials and the server's `MULTICA_LLM_*` assist client. Do not create paid resources or widen permissions as part of this PR.

The dedicated app needs published `im.message.receive_v1`, approved `im:message.group_msg` for all group messages, message/history and uploaded-resource read access, native-message reply capability and the ability to list this group's members. Verify each using that app's identity. Another app's CLI scope list is not evidence. The live app inspected on 8 October 2026 used persistent connection and receive-message events; all-group-message permission was not present in the inspected scope list. Activation remains pending scope and runtime proof.

Set `MULTICA_LARK_KNOWLEDGE_POLICY` to one JSON object, initially in `capture` mode:

```json
{
  "mode": "capture",
  "installation_id": "<existing installation UUID>",
  "app_id": "<existing cli_ app ID>",
  "workspace_id": "<private workspace UUID>",
  "agent_id": "<existing private agent UUID>",
  "project_id": "<existing hiring project UUID>",
  "owner_id": "<installation owner UUID>",
  "chat_id": "<allowed oc_ group ID>",
  "start_time": "2026-10-08T00:00:00Z",
  "model": "<explicit authorized model>",
  "daily_model_calls": 20
}
```

Replace placeholders; the sample timestamp and cap are examples, not production approval. `start_time` is the first history capture boundary. Keep it stable through cutover. Blank policy disables the feature. Malformed/incomplete policy or missing Lark setup fails startup; `process` also requires enabled assist and object storage. The hiring board must already have Record type/Candidate, Review status/Assessed, Assessment version (text), and Hiring stage/New properties. Do not use policy changes to bypass a revoked installation or owner permission.

The model receives at most 256 KiB of extracted file text plus filename for each uncached digest, or an 8 KiB question plus six sources of at most 5000 runes each. No model is used for ordinary message capture or history reconciliation. The daily cap counts reserved invocations per database date, including failed invocations; SDK transport/compatibility retries can create more requests. It is not a dollar budget. Confirm the endpoint's data handling and cost boundary before enabling `process`.

Runtime dependencies: Poppler (`pdfinfo`, `pdftotext`, `pdftoppm`) and Tesseract with English/Vietnamese language data, installed by the server Dockerfile. Download limit is 20 MiB, PDF limit 60 pages, OCR limit 20 pages, extracted text limit 256 KiB and parser timeout 60 seconds. DOCX expansion is bounded. Unsupported, corrupt, oversized or ambiguous files quarantine with a private actionable error.

## Durable processing and recovery

File stages: received -> downloaded -> extracted -> classified -> candidate-updated -> acknowledgement-queued -> acknowledgement-sent -> complete. Each step commits before the next. File digest keys reuse extraction and assessment across reuploads; message records retain each source. The single-installation advisory lock serializes workers. A restart resumes stored stages. Original bytes remain available even when assessment fails. Workspace deletion removes the knowledge records in its existing teardown transaction; ingestion and bounded worker steps hold a workspace read lock to prevent recreation during teardown.

Acknowledgements and Q&A use durable jobs, `reply_in_thread=true` and the job UUID. There is no main-group fallback. The first possible send time is stored before HTTP. Retries reuse the UUID for at most 50 minutes because Lark only guarantees one-hour duplicate suppression. Older uncertain deliveries quarantine; inspect the original thread before any operator retry. Do not clear `first_send_at` or invent a new UUID to force a resend. This is bounded idempotency, not an exactly-once delivery claim.

Reconciliation runs every 15 minutes with a one-hour overlap, paginates history and known threads, and re-fetches known messages for edits/recalls. It advances the watermark only after a complete pass. Authoritative absence/deletion marks the source unavailable; old receive replays cannot revive it. An authoritative restoration can resume cancelled file work, but never replays completed acknowledgements or historical questions. Transport/permission errors retain the watermark; queries fail closed when the current source cannot be checked. Large history beyond the bounded page/time limits needs operator investigation; failure is visible, never treated as full coverage.

## Verification and cutover

1. Apply additive migrations through the normal runner. Concurrent indexes each have their own migration. Keep the policy blank until runtime prerequisites are checked.
2. Enable `capture` on the existing connection. Verify an authorized new event and its source/job rows, full history pagination, old-thread replies and watermark recovery after a controlled outage. No production model or messaging test is implicit in this step.
3. Reconcile pending upload message/digest provenance against the existing monitor's candidate records. Do not run two candidate writers concurrently: the capture phase overlaps the monitor; the `process` transition needs an explicit coordinated handoff. The source marker supports retry recovery, but it is not a cross-system distributed lock.
4. At an authorized cutover, finish the monitor's in-flight cycle, pause its writes, then enable `process`. Prove one useful genuine upload end-to-end, including original attachment, a single candidate update, one native-thread ACK, and a member's sourced question. Keep private-data isolation and actual group membership checks in that verification. Local fixtures do not prove this live path.
5. Verify cloud reconciliation during an outage before removing laptop dependence. If processing fails, roll back to `capture` and reconcile any uncertain sends/writes before resuming the monitor. The existing heartbeat is unchanged by this PR.

## Operations

Run these read-only queries using the deployment's already-authorized database access. Bind `:installation_id` to the configured installation. Results are private operational data, not group-chat status messages.

```sql
SELECT history_through, last_reconciled_at, next_reconcile_at, last_error,
       model_day, model_calls
FROM lark_knowledge_state WHERE installation_id = :installation_id;

SELECT kind, stage, count(*), min(created_at) AS oldest_job,
       max(attempts) AS max_attempts
FROM lark_knowledge_job WHERE installation_id = :installation_id
GROUP BY kind, stage ORDER BY kind, stage;

SELECT id, kind, stage, last_error, updated_at
FROM lark_knowledge_job
WHERE installation_id = :installation_id AND stage = 'quarantined'
ORDER BY updated_at DESC;

SELECT max(received_at) AS latest_capture,
       now() - max(received_at) AS time_since_capture
FROM lark_knowledge_event WHERE installation_id = :installation_id;
```

Time since capture is a freshness signal, not proof that a quiet group lost events. For actual receive lag, compare a verified source event's `CreateTime` (Unix milliseconds inside normalized event JSON) with `received_at`. Snapshot events are reconciliation reads, not fresh deliveries. Alert on new quarantines, a missed reconciliation interval with `last_error`, or verified event lag; avoid routine bot status chatter. Built-in logs report only new reconciliation failure transitions and quarantines, without source text or raw provider errors. No external alert destination is configured here.

Retry only after resolving the recorded cause and verifying the source/candidate/thread state. For a file job, determine its last durable stage from `state.file_id` and the extraction/assessment tables; preserve state and return it to that stage with attempts reset and `available_at=now()`. For Q&A older than 15 minutes, leave it cancelled and use a new question. For an uncertain ACK, reconcile the actual thread first; a confirmed delivered message can be recorded as complete, otherwise require an operator decision. Never bulk reset all jobs.

Rollback: change `process` to `capture` and restart the API to stop knowledge writes/replies while retaining capture/recovery. Blank policy stops capture too. Preserve the six knowledge tables and originals for recovery/audit; down migrations destroy that data and are not the operational rollback. Access revocation disables processing and group answers but intentionally retains originals and private candidate evidence; deletion/retention cleanup requires a separate authorized data operation.

## Local verification

Use the managed checkout environment and synthetic inputs only. Stop the checkout API before database-backed suites: its background workers can claim test fixtures. `make down` preserves the managed database.

```sh
make down
make env-exec ARGS='-- go -C server test ./internal/integrations/lark ./internal/integrations/channel/engine -count=1'
```

The integration tests cover concurrent duplicate events, worker restart, digest deduplication, lost-send UUID reuse, expiry, source recall/restoration, private-data isolation, membership loss, history pagination/old threads, failed watermark retention, ambiguous identity and model budgets. Pure tests cover bounded malformed/scanned PDF/DOCX extraction, citation validation and native reply parameters. No candidate files, live model calls or agent CLI smoke tests are used.

Optional installed-parser smoke (synthetic text and scanned PDFs; no model/network calls):

```sh
MULTICA_RUN_PARSER_SMOKE=1 go -C server test -tags=parserintegration ./internal/integrations/lark -run TestKnowledgeInstalledPDFParsers -count=1
```
