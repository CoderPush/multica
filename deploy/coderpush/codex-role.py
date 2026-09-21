#!/usr/bin/python3
"""Run one web role with its own credentials and a macOS filesystem sandbox."""
import datetime
import json
import os
import re
from pathlib import Path
import sys

ROOT = Path('/Users/webworker')
role = {'codex-engineer': 'engineer', 'codex-reviewer': 'reviewer'}.get(Path(sys.argv[0]).name)
if role is None:
    sys.exit('Use the engineer or reviewer launcher.')
home = ROOT / 'runtime' / role
codex = ROOT / 'bin/codex'
if sys.argv[1:] == ['--version']:
    os.execv(str(ROOT / 'bin/node'), [str(ROOT / 'bin/node'), str(codex), '--version'])
cwd = Path.cwd().resolve()
tasks = ROOT / 'tasks'
if tasks not in cwd.parents:
    sys.exit('Role must start inside its managed task workspace.')

# Preserve daemon-generated skills, configuration and session logs per task.
requested_home = os.environ.get('CODEX_HOME')
task_home = Path(requested_home).resolve() if requested_home else home
if requested_home:
    if task_home != cwd.parent / 'codex-home' or tasks not in task_home.parents:
        sys.exit('Unexpected task Codex home.')
    task_home.mkdir(exist_ok=True)
    auth = task_home / 'auth.json'
    role_auth = home / 'auth.json'
    if auth.is_symlink():
        if auth.resolve() != role_auth.resolve():
            sys.exit('Unexpected task authentication link.')
    elif auth.exists():
        sys.exit('Unexpected task authentication file; inspect before replacing.')
    else:
        auth.symlink_to(role_auth)
    # Keep only the existing scoped role credentials and runtime paths in tools.
    config = task_home / 'config.toml'
    if config.exists():
        config_text = config.read_text()
        match = re.search(r"(?m)^include_only = \[(.*?)\]$", config_text)
        if match:
            extra = ['GH_TOKEN', 'GIT_CONFIG_*', 'PLAYWRIGHT_BROWSERS_PATH',
                     'XDG_CACHE_HOME', 'AGENT_BROWSER_EXECUTABLE_PATH', 'AGENT_BROWSER_SESSION']
            value = match.group(1)
            for key in extra:
                if repr(key) not in value and json.dumps(key) not in value:
                    value += ', ' + repr(key)
            config.write_text(config_text[:match.start(1)] + value + config_text[match.end(1):])

def quote(value):
    return json.dumps(str(value))

policy = '(version 1)\n(allow default)\n'
policy += '(deny file-read* (subpath "/Users") (subpath "/Volumes"))\n'
policy += '(allow file-read-metadata (subpath "/Users"))\n(deny file-write*)\n'
for path in [ROOT / 'tools', ROOT / 'bin']:
    policy += '(allow file-read* (subpath ' + quote(path) + '))\n'
for path in [cwd, home, task_home, ROOT / 'cache' / role, Path('/private/tmp'), Path('/private/var/folders'), Path('/dev')]:
    policy += '(allow file-read* file-write* (subpath ' + quote(path) + '))\n'
config_root = os.environ.get('MULTICA_TASK_CONFIG_ROOT')
if config_root:
    path = Path(config_root).resolve()
    if tasks not in path.parents or cwd.parent not in path.parents:
        sys.exit('Unexpected task configuration path.')
    policy += '(allow file-read* file-write* (subpath ' + quote(path) + '))\n'
policy += '(allow file-read* (subpath ' + quote(ROOT / 'browsers') + '))\n'
env = {k: v for k, v in os.environ.items() if k.startswith('MULTICA_') or k in ('LANG', 'TERM', 'TMPDIR', 'XPC_FLAGS', 'XPC_SERVICE_NAME')}
env.update(HOME=str(home), CODEX_HOME=str(task_home), USER='webworker',
           PATH=str(ROOT / 'bin') + ':/usr/bin:/bin:/usr/sbin:/sbin',
           PLAYWRIGHT_BROWSERS_PATH=str(ROOT / 'browsers'),
           XDG_CACHE_HOME=str(ROOT / 'cache' / role),
           AGENT_BROWSER_EXECUTABLE_PATH=str(ROOT / 'browsers/chromium_headless_shell-1223/chrome-headless-shell-mac-x64/chrome-headless-shell'),
           AGENT_BROWSER_SESSION=role + '-' + cwd.parent.name,
           MULTICA_REPO_CHECKOUT_MODE='isolated')
token_file = home / 'github-token.json'
if token_file.exists():
    token = json.loads(token_file.read_text())
    expires = datetime.datetime.fromisoformat(token['expires_at'].replace('Z', '+00:00'))
    if expires <= datetime.datetime.now(datetime.timezone.utc):
        sys.exit('Role GitHub token expired; check the renewal transport.')
    env.update(GH_TOKEN=token['token'], GIT_CONFIG_COUNT='2',
               GIT_CONFIG_KEY_0='credential.https://github.com.helper',
               GIT_CONFIG_VALUE_0='!f() { echo username=x-access-token; echo password=$GH_TOKEN; }; f',
               GIT_CONFIG_KEY_1='credential.https://github.com.useHttpPath', GIT_CONFIG_VALUE_1='true')
if sys.argv[1:] == ['--boundary-check']:
    command = ['/bin/sh', '-c', 'test ! -r /Users/hogan/.codex/auth.json && test ! -r /Users/webworker/.multica/config.json && test ! -r /Users/webworker/runtime/' + ('reviewer' if role == 'engineer' else 'engineer') + '/auth.json && echo BOUNDARIES_OK']
elif sys.argv[1:] == ['--github-check']:
    command = [str(ROOT / 'bin/gh'), 'api', 'repos/CoderPush/web', '--jq', '{full_name,permissions}']
elif len(sys.argv) > 2 and sys.argv[1] == '--exec':
    command = sys.argv[2:]
else:
    command = [str(codex), *sys.argv[1:]]
os.execve('/usr/bin/sandbox-exec', ['sandbox-exec', '-p', policy, *command], env)
