# Lark group ingress relay

The optional relay forwards literal messages from one configured group to an operator-owned durable receiver. It reuses the native connection. It introduces no worker, storage schema, model, business policy or document parser.

Leave `MULTICA_LARK_INGRESS_RELAY` empty to retain current behavior. Configure a JSON object with exactly these fields:

| Field | Meaning |
| --- | --- |
| `installation_id` | Active native installation UUID |
| `app_id` | The installation's app ID (`cli_...`) |
| `chat_id` | The opted-in group (`oc_...`) |
| `endpoint` | Trusted HTTPS receiver URL; plaintext is permitted only on loopback for local testing |
| `secret` | Random shared secret of at least 32 bytes; keep it in deployment secrets |
| `disposition` | `observe` forwards then continues native routing; `consume` forwards and stops native routing for the group |

The receiver must already be available and able to commit before enabling the relay. This configuration does not grant provider permissions or authorize new access. DMs and nonmatching groups retain their native route. In consume mode, the receiver owns matching bot and unsupported events as well; it must acknowledge them deliberately without opening a private-agent route.

## HTTP contract (version 1)

The relay POSTs JSON with `version: 1`, `installation_id`, `workspace_id`, `agent_id`, `app_id`, `event_id`, `event_type`, `chat_id`, `chat_type`, `message_id`, `sender_id`, `sender_type`, `message_type`, `content`, `create_time`, `parent_id`, `root_id`, `thread_id`, `addressed_to_bot` and `command_body`. IDs and time values are strings; `addressed_to_bot` is boolean. `content` is the literal provider JSON string. The payload excludes enriched quotes/history, private sessions and installation credentials. All source text is untrusted evidence.

`X-Multica-Timestamp` is Unix seconds. `X-Multica-Signature` is the lowercase hex HMAC-SHA256 of `timestamp + "." + exact_request_body`, keyed by the shared secret. Verify the signature with constant-time comparison, check a bounded timestamp window and revalidate every configured identity pin. HTTPS authenticates the receiver and protects both messages and receipts.

Commit the event idempotently before responding. Return a 2xx status with:

```json
{"version":1,"event_id":"the-request-event-id","durable":true,"disposition":"consume"}
```

The receipt disposition must match the configured value, including on duplicate events. An observe receipt cannot reopen native routing when consume is configured. Unsupported version, missing durable confirmation, mismatched ID/mode, non-2xx response or transport failure returns an error to the existing connector, which NACKs and reconnects. There is no fallthrough on failure and no group-root response from this relay.

Requests are limited to 512 KiB and two seconds; receipts to 4 KiB. Redirects are rejected. Keep the receiver's synchronous work to authentication, validation and durable commit. OCR, model calls and external writes belong after that commit. Provider retries are finite: receivers need independent reconciliation and must tolerate commit-success/response-loss replays.

## Verification and rollout

Use a local fake receiver to verify signature, durable duplicate handling, observe/consume matching, timeout and rejection behavior. Verify an active installation and actual durable receipt before an authorized production cutover. Coordinate mode changes; mismatched modes intentionally interrupt delivery. Receiver outages never select observe automatically. Stopping forwarding or returning to observe is an explicit operator routing decision.
