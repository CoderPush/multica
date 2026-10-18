#!/usr/bin/python3 -I
"""Install only the Hogan webworker runtime repair, with preflight and rollback."""
import argparse
import datetime
import json
import os
from pathlib import Path
import plistlib
import pwd
import re
import stat
import subprocess
import tempfile
import time

ROOT = Path('/Users/webworker')
LABEL = 'com.coderpush.webworker.worker'
PLIST = Path('/Library/LaunchDaemons') / (LABEL + '.plist')
ENTRY = Path('/usr/local/libexec/coderpush-webworker-launch.py')
BACKUPS = Path('/var/backups/coderpush-webworker')
SOURCES = {'worker-launch.py': ENTRY}
TARGETS = list(SOURCES.values()) + [PLIST]


def run(args, **kwargs):
    return subprocess.run(args, check=True, text=True, timeout=40,
                          stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kwargs)


def status():
    result = run(['/usr/bin/sudo', '-H', '-u', 'webworker', '--',
                  str(ROOT / 'bin/multica'), '--profile', 'hogan-web',
                  'daemon', 'status', '--output', 'json'])
    return json.loads(result.stdout)


def require_idle(info):
    for key in ('active_task_count', 'running_task_count', 'resource_wait_task_count'):
        if info.get(key) != 0:
            raise RuntimeError('Worker is busy or status is incomplete: ' + key)


def configuration(original):
    expected = [str(ROOT / 'bin/multica'), '--profile', 'hogan-web', 'daemon', 'start',
                '--foreground', '--device-name', 'Hogan Web Worker',
                '--workspaces-root', str(ROOT / 'tasks'), '--max-concurrent-tasks', '2',
                '--no-auto-update']
    installed = ['/usr/bin/python3', '-I', str(ENTRY)]
    if original.get('Label') != LABEL or original.get('ProgramArguments') not in (expected, installed):
        raise RuntimeError('Unexpected LaunchDaemon configuration; inspect before changing it.')
    if original['ProgramArguments'] == expected:
        if original.get('UserName') != 'webworker' or original.get('GroupName') != 'webworker':
            raise RuntimeError('Unexpected worker service identity.')
    elif original.get('UserName') or original.get('GroupName'):
        raise RuntimeError('Installed root entrypoint has an unexpected service identity.')
    updated = dict(original)
    updated.pop('UserName', None)
    updated.pop('GroupName', None)
    updated['ProgramArguments'] = installed
    updated['EnvironmentVariables'] = dict(original.get('EnvironmentVariables', {}),
                                           MULTICA_REPO_CHECKOUT_MODE='isolated')
    return updated


def protected_source(directory):
    # Apply only from an administrator-owned bundle, never a worker-writable tree.
    allowed = {0, int(os.environ.get('SUDO_UID', os.getuid()))}
    for path in [directory, *directory.parents, *(directory / name for name in SOURCES)]:
        info = path.lstat()
        if path.is_symlink() or info.st_uid not in allowed or info.st_mode & 0o022:
            raise RuntimeError('Unsafe installer source ownership or permissions: ' + str(path))


def atomic(path, data, mode, uid, gid):
    if path.is_symlink():
        raise RuntimeError('Refusing symlink target: ' + str(path))
    fd, name = tempfile.mkstemp(prefix='.worker-runtime-', dir=path.parent)
    try:
        with os.fdopen(fd, 'wb') as output:
            output.write(data)
            output.flush()
            os.fsync(output.fileno())
            os.fchmod(output.fileno(), mode)
            os.fchown(output.fileno(), uid, gid)
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def backup():
    BACKUPS.mkdir(mode=0o700, parents=True, exist_ok=True)
    if BACKUPS.is_symlink() or BACKUPS.stat().st_uid != 0 or BACKUPS.stat().st_mode & 0o077:
        raise RuntimeError('Unsafe backup directory.')
    directory = Path(tempfile.mkdtemp(prefix=datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%SZ-'), dir=BACKUPS))
    records = []
    for index, path in enumerate(TARGETS):
        record = {'path': str(path), 'exists': path.exists()}
        if path.is_symlink():
            raise RuntimeError('Refusing symlink target: ' + str(path))
        if path.exists():
            info = path.stat()
            record.update(mode=stat.S_IMODE(info.st_mode), uid=info.st_uid, gid=info.st_gid, file=str(index))
            (directory / str(index)).write_bytes(path.read_bytes())
        records.append(record)
    (directory / 'manifest.json').write_text(json.dumps(records, indent=2))
    return directory


def load_backup(directory):
    if directory.parent != BACKUPS or directory.is_symlink():
        raise RuntimeError('Rollback must name an original backup directory.')
    info = directory.stat()
    if info.st_uid != 0 or info.st_mode & 0o077:
        raise RuntimeError('Unsafe rollback ownership or permissions.')
    records = json.loads((directory / 'manifest.json').read_text())
    if [record['path'] for record in records] != [str(path) for path in TARGETS]:
        raise RuntimeError('Unexpected rollback targets.')
    for index, record in enumerate(records):
        if record['exists']:
            record['data'] = (directory / str(index)).read_bytes()
            for key in ('mode', 'uid', 'gid'):
                if not isinstance(record[key], int):
                    raise RuntimeError('Invalid backup metadata.')
    return records


def restore(directory):
    for record in load_backup(directory):
        path = Path(record['path'])
        if record['exists']:
            atomic(path, record['data'], record['mode'], record['uid'], record['gid'])
        elif path.exists():
            if path.is_symlink():
                raise RuntimeError('Refusing rollback symlink: ' + str(path))
            path.unlink()


def bootout():
    run(['/bin/launchctl', 'bootout', 'system/' + LABEL])


def service_state():
    try:
        output = run(['/bin/launchctl', 'print', 'system/' + LABEL]).stdout
    except subprocess.CalledProcessError as error:
        if error.returncode == 113 and 'Could not find service' in error.stderr:
            return False, None
        raise
    match = re.search(r'(?m)^\s*pid = (\d+)\s*$', output)
    return True, int(match.group(1)) if match else None


def rollback_allowed(info):
    loaded, pid = service_state()
    if info.get('status') == 'running':
        require_idle(info)
    elif info.get('status') != 'stopped' or pid:
        raise RuntimeError('Cannot prove stopped worker is safe to roll back; reconcile its process first.')
    return loaded


def bootstrap():
    run(['/bin/launchctl', 'bootstrap', 'system', str(PLIST)])


def wait_running(previous_pid):
    deadline = time.monotonic() + 30
    while time.monotonic() < deadline:
        try:
            info = status()
            pid = info.get('pid')
            if info.get('status') == 'running' and pid and pid != previous_pid:
                uid = run(['/bin/ps', '-p', str(pid), '-o', 'uid=']).stdout.strip()
                if uid != '600':
                    raise RuntimeError('Worker daemon did not drop root privileges.')
                return info
        except (subprocess.SubprocessError, json.JSONDecodeError):
            pass
        time.sleep(1)
    raise RuntimeError('Worker did not restart successfully within 30 seconds.')


def install(directory, before, updated):
    saved = backup()
    service_stopped = False
    try:
        for name, target in SOURCES.items():
            atomic(target, (directory / name).read_bytes(), 0o755, 0, 0)
        for role in ('engineer', 'reviewer'):
            workdir = ROOT / 'tasks/worker-runtime-preflight' / role / 'workdir'
            # Never write or chown through a worker-controlled path as root.
            run(['/usr/bin/sudo', '-H', '-u', 'webworker', '--',
                 '/bin/mkdir', '-p', str(workdir)])
            result = run(['/usr/bin/python3', '-I', str(ENTRY), '--preflight-' + role])
            print(result.stdout.strip(), flush=True)
        require_idle(status())
        service_stopped = True
        bootout()
        atomic(PLIST, plistlib.dumps(updated), 0o644, 0, 0)
        bootstrap()
        info = wait_running(before.get('pid'))
        print(json.dumps({'result': 'installed', 'backup': str(saved), 'status': info}))
    except BaseException:
        if service_stopped:
            # bootstrap may have succeeded even when later health verification failed.
            try:
                if service_state()[0]:
                    bootout()
            except subprocess.SubprocessError as recovery_error:
                # Restore files even if launchd cannot finish stopping its job.
                restore(saved)
                raise RuntimeError('Service shutdown failed during recovery. Original files restored; '
                                   'reconcile launchd before restarting. Backup: ' + str(saved)) from recovery_error
        restore(saved)
        if service_stopped:
            bootstrap()
        print('Installation failed; original files restored. Backup: ' + str(saved), flush=True)
        raise


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    action = parser.add_mutually_exclusive_group()
    action.add_argument('--apply', action='store_true')
    action.add_argument('--rollback', type=Path)
    args = parser.parse_args()
    directory = Path(__file__).absolute().parent
    original = plistlib.loads(PLIST.read_bytes())
    updated = configuration(original)
    account = pwd.getpwnam('webworker')
    if (account.pw_uid, account.pw_gid, account.pw_dir) != (600, 600, str(ROOT)):
        raise RuntimeError('Unexpected webworker account.')
    if not args.apply and not args.rollback:
        print(json.dumps({'mode': 'check', 'service': LABEL, 'account': 'webworker',
                          'targets': [str(path) for path in TARGETS],
                          'preflight': ['engineer', 'reviewer'], 'automaticRollback': True}))
        return
    if os.geteuid() != 0:
        raise RuntimeError('--apply and --rollback require OS administrator authorization.')
    protected_source(directory)
    before = status()
    if args.rollback:
        selected = args.rollback.absolute()
        load_backup(selected)
        loaded = rollback_allowed(before)
        if loaded:
            bootout()
        restore(selected)
        bootstrap()
        print(json.dumps({'rollback': str(selected), 'status': wait_running(before.get('pid'))}))
        return
    require_idle(before)
    ENTRY.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
    for path in [ENTRY.parent, *ENTRY.parent.parents]:
        if path.is_symlink() or path.stat().st_uid != 0 or path.stat().st_mode & 0o022:
            raise RuntimeError('Root entrypoint parent is writable by another account: ' + str(path))
    install(directory, before, updated)


if __name__ == '__main__':
    main()
