#!/usr/bin/python3
import os
import json
import re
from datetime import datetime, timezone
import sys
from pathlib import Path

root = Path('/srv/multica-worker')
role = {'codex-seo': 'seo', 'codex-engineer': 'engineer', 'codex-reviewer': 'reviewer'}.get(Path(sys.argv[0]).name, 'web')
codex = '/opt/multica-agent-tools/node_modules/.bin/codex'
home = root / 'runtime' / (role + '-home')
args = ['/usr/bin/bwrap', '--die-with-parent', '--new-session', '--unshare-pid',
        '--ro-bind', '/', '/', '--proc', '/proc', '--dev', '/dev',
        '--tmpfs', '/tmp', '--tmpfs', '/home', '--tmpfs', '/root',
        '--tmpfs', '/run', '--tmpfs', '/opt/multica',
        '--ro-bind', '/run/systemd/resolve', '/run/systemd/resolve',
        '--tmpfs', str(root / 'runtime'),
        '--bind', str(home), str(home),
        '--bind', str(root / 'tasks'), str(root / 'tasks')]
if role == 'seo':
    args += ['--tmpfs', str(root / 'repos')]
if role in ('web', 'seo', 'engineer', 'reviewer'):
    cwd = Path.cwd().resolve()
    tasks = root / 'tasks'
    args += ['--tmpfs', str(tasks)]
    if cwd.is_relative_to(tasks) and len(cwd.relative_to(tasks).parts) >= 3:
        task_root = tasks.joinpath(*cwd.relative_to(tasks).parts[:2])
        args += ['--bind', str(task_root), str(task_root)]
    cache = tasks / '.skill-cache'
    if cache.exists():
        args += ['--ro-bind', str(cache), str(cache)]
# Preserve the daemon's task-local skills, config and rollout directory.
requested_home = os.environ.get('CODEX_HOME')
task_home = Path(requested_home).resolve() if requested_home else home
if requested_home:
    cwd = Path.cwd().resolve()
    if task_home != cwd.parent / 'codex-home' or not task_home.is_relative_to(root / 'tasks'):
        sys.exit('Unexpected task Codex home.')
    task_home.mkdir(exist_ok=True)
    auth = task_home / 'auth.json'
    role_auth = home / 'auth.json'
    if auth.is_symlink():
        if auth.resolve() != role_auth.resolve():
            sys.exit('Unexpected task authentication link.')
    elif auth.exists():
        sys.exit('Unexpected task authentication file.')
    else:
        auth.symlink_to(role_auth)
    config = task_home / 'config.toml'
    if config.exists():
        text = config.read_text()
        match = re.search(r"(?m)^include_only = \[(.*?)\]$", text)
        if match:
            value = match.group(1)
            for key in ['GH_TOKEN', 'GIT_CONFIG_*', 'GOOGLE_APPLICATION_CREDENTIALS', 'GA4_PROPERTY_ID', 'GSC_SITE_URL']:
                if repr(key) not in value and json.dumps(key) not in value:
                    value += ', ' + repr(key)
            config.write_text(text[:match.start(1)] + value + text[match.end(1):])
env = {k: v for k, v in os.environ.items() if k in ['LANG', 'TERM'] or k.startswith('MULTICA_')}
env.update(HOME=str(home), CODEX_HOME=str(task_home), USER='multica-worker',
           PATH='/opt/node22/bin:/usr/local/bin:/usr/bin:/bin', TMPDIR='/tmp')
if role == 'seo':
    env.update(GOOGLE_APPLICATION_CREDENTIALS=str(home / 'google-service-account.json'),
               GA4_PROPERTY_ID='461631613', GSC_SITE_URL='sc-domain:coderpush.com')
token_path = home / 'github-token.json'
if role != 'seo' and token_path.exists():
    token = json.loads(token_path.read_text())
    if datetime.fromisoformat(token['expires_at'].replace('Z', '+00:00')) <= datetime.now(timezone.utc):
        sys.exit('GitHub credential expired; ask operator to check multica-github-tokens.timer')
    env.update(GH_TOKEN=token['token'], GIT_CONFIG_COUNT='2',
               GIT_CONFIG_KEY_0='credential.https://github.com.helper',
               GIT_CONFIG_VALUE_0='!f() { echo username=x-access-token; echo password=$GH_TOKEN; }; f',
               GIT_CONFIG_KEY_1='credential.https://github.com.useHttpPath', GIT_CONFIG_VALUE_1='true')
if '--pilot-github-check' in sys.argv:
    args += ['/usr/bin/gh', 'api', 'repos/CoderPush/web', '--jq', '{full_name,default_branch}']
elif '--pilot-boundary-check' in sys.argv:
    args += ['/bin/sh', '-c',
             'test ! -r /opt/multica/.env && test ! -r /home/multica-worker/.multica/profiles/server/config.json && echo SERVER_SECRETS_BLOCKED; '
             'if test -r /srv/multica-worker/repos/web/README.md; then echo REPO_READABLE; else echo REPO_BLOCKED; fi']
else:
    args += [codex, *sys.argv[1:]]
os.execve(args[0], args, env)
