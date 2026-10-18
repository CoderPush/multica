#!/usr/bin/python3 -I
"""Fixed root-owned maintenance capability for the Hogan worker operator."""
import fcntl
import importlib.util
import json
import os
from pathlib import Path
import stat
import sys

HERE = Path('/usr/local/libexec/coderpush-webworker')
ACTIONS = ('status', 'apply', 'rollback')


def load_installer():
    spec = importlib.util.spec_from_file_location('worker_installer', HERE / 'install-worker-runtime.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def latest_backup(installer):
    candidates = sorted(path for path in installer.BACKUPS.iterdir()
                        if path.is_dir() and not path.is_symlink() and (path / 'manifest.json').is_file())
    if not candidates:
        raise RuntimeError('No worker repair backup is available.')
    chosen = candidates[-1]
    installer.load_backup(chosen)
    return chosen


def main():
    if os.geteuid() != 0:
        sys.exit('Use the authorized sudo maintenance command.')
    if len(sys.argv) != 2 or sys.argv[1] not in ACTIONS:
        sys.exit('Expected exactly one action: status, apply or rollback.')
    action = sys.argv[1]
    # Do not inherit Python hooks, worker HOME, paths or an operator's credentials.
    os.environ.clear()
    os.environ.update(HOME='/var/root', PATH='/usr/bin:/bin:/usr/sbin:/sbin', LANG='C')
    descriptor = os.open(HERE / 'maintenance.lock', os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'a') as lock:
        info = os.fstat(lock.fileno())
        if info.st_uid != 0 or not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
            raise RuntimeError('Unsafe maintenance lock ownership or permissions.')
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        installer = load_installer()
        if action == 'status':
            info = installer.status()
            print(json.dumps({key: info.get(key) for key in
                              ('status', 'pid', 'active_task_count', 'running_task_count',
                               'resource_wait_task_count')}))
            return
        args = ['--apply'] if action == 'apply' else ['--rollback', str(latest_backup(installer))]
        sys.argv = [str(HERE / 'install-worker-runtime.py'), *args]
        installer.main()


if __name__ == '__main__':
    main()
