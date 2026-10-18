#!/usr/bin/python3 -I
"""Enter webworker's macOS session, then drop root before running worker code."""
import os
from pathlib import Path
import pwd
import sys


ROOT = Path('/Users/webworker')
WORKER_ARGS = [
    str(ROOT / 'bin/multica'), '--profile', 'hogan-web', 'daemon', 'start',
    '--foreground', '--device-name', 'Hogan Web Worker',
    '--workspaces-root', str(ROOT / 'tasks'), '--max-concurrent-tasks', '2',
    '--no-auto-update',
]


def command(uid, args):
    # sudo is executed while root, but everything after its -- runs as webworker.
    return [
        '/bin/launchctl', 'asuser', str(uid), '/usr/bin/sudo', '-H', '-u',
        'webworker', '--', '/usr/bin/env', 'HOME=' + str(ROOT),
        'PATH=' + str(ROOT / 'bin') + ':/usr/bin:/bin:/usr/sbin:/sbin',
        'MULTICA_REPO_CHECKOUT_MODE=isolated', *args,
    ]


def main():
    if os.geteuid() != 0:
        sys.exit('This root-owned LaunchDaemon entrypoint must run as root.')
    account = pwd.getpwnam('webworker')
    if account.pw_uid != 600 or account.pw_gid != 600 or Path(account.pw_dir) != ROOT:
        sys.exit('Unexpected webworker account; refusing to launch.')
    args = sys.argv[1:]
    if not args:
        argv = command(account.pw_uid, WORKER_ARGS)
    elif len(args) == 1 and args[0] in ('--preflight-engineer', '--preflight-reviewer'):
        role = args[0].removeprefix('--preflight-')
        workdir = ROOT / 'tasks/worker-runtime-preflight' / role / 'workdir'
        os.chdir(workdir)
        argv = command(account.pw_uid, [
            str(ROOT / 'bin' / ('codex-' + role)), '--exec',
            str(ROOT / 'bin/node'), str(ROOT / 'bin/browser-smoke.cjs'),
            '--role', role,
        ])
    else:
        sys.exit('Unsupported arguments.')
    os.execv(argv[0], argv)


if __name__ == '__main__':
    main()
