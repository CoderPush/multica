# Upstream upgrade — 24 September 2026

## Release identity

- Upstream: `e909e9c89d524cd54c1d5f4fa5963882093efdc9` (latest main fetched at preparation).
- Fork release: `355006fc62cc379eabeb61b2a9ee50e2707e2dd7`, [PR #9](https://github.com/CoderPush/multica/pull/9).
- Preserves the duplicate-assignee handoff guard and acceptance-based parent completion. Merge resolution also preserves upstream cancellation/dependency warnings.
- Previous backend: `013385718f731be728fd7b2c0d09828edb868009`, v0.4.43 handoff backport; previous frontend: v0.4.43.
- Schema transition: 467 to 547, 77 new migration files. Image-only rollback is unsafe.

## Verification

- [PR CI](https://github.com/CoderPush/multica/actions/runs/35971843482) and [release-commit CI](https://github.com/CoderPush/multica/actions/runs/35972509064) passed.
- [Backend and frontend builds](https://github.com/CoderPush/multica/actions/runs/35972515607) passed for the same release SHA.
- Focused database-backed handoff and child-completion race tests passed, with verbose output proving execution. Full local handler suite passed. The full local Go invocation hit Dsh-probe timeouts under load; its isolated race rerun passed. This is not a claim that the complete local invocation passed.
- Focused integration source review and Amazon Q review found no concrete blockers; this is not a new exhaustive audit of all upstream changes.
- A fresh production backup was restored on Singapore into an internal Docker network. All migrations passed; the copy retained 6 workspaces, 106 issues and 46 agents, with no invalid indexes.
- Rehearsal schedules were disabled, no production integration keys or workers were supplied, and network egress was blocked. Both final images then passed backend health/readiness and frontend HTTP smoke checks.
- macOS source archives must use `COPYFILE_DISABLE=1 tar --no-xattrs` for migrations. Metadata sidecars named `._*.up.sql` are otherwise mistaken for migrations. The first rehearsal caught this before applying the upgrade; the clean archive passed.

## Private image transfer

GHCR created private packages. Singapore has no registry credentials. [PR #10](https://github.com/CoderPush/multica/pull/10) adds a manual export workflow using only the Actions token's package-read permission. [Export run](https://github.com/CoderPush/multica/actions/runs/35973305156) passed. The artifact expires after three days; retain the verified images and recovery archives on the host.

Archive SHA-256 was checked before and after transfer. Registry digests matched the build logs. The archive's config hashes matched the runner's recorded image IDs; the imported OCI manifest hashes and root filesystem layer hashes were then verified on Singapore, along with AMD64/Linux and the full revision label.

Docker 29's containerd image store reports the imported OCI manifest hash as its image ID, whereas the export runner recorded config hashes. These differ without a payload change. Do not compare these two representations directly or assume `docker save/load` preserves the registry index digest.

| Image | Registry index digest | Imported manifest / local image ID |
| --- | --- | --- |
| Backend | `sha256:64b1b5376e18f3f175d8ab85d707f2564c414082741a6a16d1e4cf4bcdda2fd7` | `sha256:42f0f2f6cf0b716b5cc99c90de6800f0b9a37e012904e757eb5193d1efe5165f` |
| Frontend | `sha256:f856eee7a2f0f1cfb5a11ec5ea09b85f66417ae6545a8b338c0d73a543cb3563` | `sha256:90e09df4734b63943d9ec3e24c7a20125420af400f808f016267f326ee705236` |

Deployment pins the imported image IDs with `pull_policy: never`. No personal or agent token was copied to the host and package visibility was not changed. `DO_NOT_TRACK=1` disables the new upstream telemetry sender.

## Cutover and recovery

Cutover completed at **08:13:28 UTC / 15:13:28 Vietnam** after the global unfinished-task count reached zero. The guard refused two earlier attempts while a task was still running; those attempts left services unchanged.

- Final database backup: `/opt/multica/backups/database-20260924T081256Z.dump`; archive listing validated. Restore rehearsal was completed against the preceding fresh backup before cutover.
- Upload archive: `/opt/multica/releases/upstream-20260924/uploads.before.tar.gz` (about 247 MiB).
- Public health reports the full release SHA; readiness reports database and migrations OK; schema is 547 and invalid-index count is zero.
- Exact pre/post-cutover counts match: 6 workspaces, 106 issues, 46 agents. The protected `.env` remained byte-identical, preserving JWT/integration keys and signup policy.
- Worker, Caddy and backup timer are active. All 79 recently online runtime registrations reconnected with the same provider counts. Signed-in runtime UI shows Singapore and Hogan Web Worker online; the pre-existing offline machine remains offline.
- Lark websocket connected. No backend error lines were observed in the initial post-cutover window; neither app container restarted.
- Existing browser session loaded the issue board and runtimes successfully. Before/after screenshots remain in the local, gitignored `.screenshots/` directory. No fresh email login, new model inference or outbound Lark message was initiated for acceptance.

`/opt/multica/compose.yml` is now pinned to the new local image IDs, with pull disabled. Its effective configuration was compared byte-for-byte as parsed JSON with the tested two-file configuration before atomic replacement. Thus an ordinary base-only Compose operation cannot accidentally revive the incompatible v0.4.43 images. The matching `coderpush-images.yml` overlay is also retained. Future registry releases may replace the IDs with verified registry digest references and restore the appropriate pull policy.

The host release directory is `/opt/multica/releases/upstream-20260924/`. Keep its protected environment, Compose, Caddy, worker-service, uploads and database backup references private. Never commit those files.

For a rollback, first stop admission and drain work, stop the worker/backend/frontend, preserve any post-upgrade writes and current configuration, then restore the pre-upgrade database together with its matching old images and uploads/configuration as needed. Retain the new database before a restore; schema downgrades cannot recover rows deleted by migration 468. Reconcile writes made since cutover before replacing production data.
