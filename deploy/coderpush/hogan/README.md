# Hogan webworker runtime

This repair preserves the dedicated UID 600 account, per-role credentials and
filesystem sandbox. It fixes two delivery blockers:

- `codex-role.py` retains `MULTICA_REPO_CHECKOUT_MODE=isolated` in Codex tool
  shells. Repositories must keep Git metadata inside the task sandbox.
- `worker-launch.py` enters webworker's own macOS session with `launchctl asuser`,
  then drops to webworker before executing any worker-owned code. Chromium needs
  this session to register its Mach service.

The root entrypoint accepts only its fixed daemon command and two role preflights.
It does not use an administrator's browser, credentials or GUI session.

## Verify and stage

Run the deterministic tests locally. They do not execute installed agent CLIs:

```sh
python3 -m unittest discover -s deploy/coderpush/hogan -p 'test_*.py' -v
```

Use the existing dedicated worker SSH identity to back up and replace only
`/Users/webworker/bin/codex-role.py` and install
`/Users/webworker/bin/browser-smoke.cjs`, owned by webworker and executable.
Do this while the worker has zero active, running and resource-wait tasks.

Stage this directory in an administrator-owned directory on Hogan. It and its
ancestors must not be writable by the worker or by a group. Check the proposed
root service change without sudo:

```sh
/usr/bin/python3 -I install-worker-runtime.py
```

## Apply the macOS service repair

An OS administrator must authorize this command on Hogan:

```sh
sudo /usr/bin/python3 -I install-worker-runtime.py --apply
```

The installer refuses a busy worker or an unexpected service/account. It backs up
only the root-owned entrypoint and plist, tests Chromium inside both role sandboxes
at desktop/mobile widths, and checks that credential boundaries still hold. Only
then does it restart the LaunchDaemon. It verifies the new daemon runs as UID 600.
On failure it restores the original files and, after confirming shutdown,
restarts the original service. If launchd cannot confirm shutdown, it restores
the files and reports the required process reconciliation instead of starting a
second daemon. No application release or Multica binary upgrade is part of this
repair.

The installer prints a root-only backup path. To restore that version later, wait
for the worker to become idle, then run:

```sh
sudo /usr/bin/python3 -I install-worker-runtime.py --rollback /var/backups/coderpush-webworker/BACKUP
```

## Acceptance

SSH smoke tests do not prove daemon task health. After installation, dispatch a
fresh diagnostic through Multica for Engineer and Reviewer. Each must use the
supported repository checkout, confirm `.git` is a directory inside its task,
run Git operations, launch Chromium, click a local fixture and save 1440/390
screenshots. Keep the negative credential checks. Reconcile existing linked
checkouts through the supported checkout command; do not delete working changes.

Then let Web Lead complete one ready delivery through current-head CI, visual
review, merge and verified production, without an operator completing its steps.
