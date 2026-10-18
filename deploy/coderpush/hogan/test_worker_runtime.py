import importlib.util
from contextlib import ExitStack, contextmanager, redirect_stdout
import io
import json
import os
from pathlib import Path
import tempfile
import subprocess
from types import SimpleNamespace
import unittest
from unittest.mock import patch


HERE = Path(__file__).parent


def load(name):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@contextmanager
def installer_mocks(module):
    with ExitStack() as stack:
        mocks = {name: stack.enter_context(patch.object(module, name)) for name in
                 ('backup', 'atomic', 'run', 'status', 'bootout', 'bootstrap',
                  'wait_running', 'restore', 'service_state')}
        mocks['backup'].return_value = Path('/safe/backup')
        mocks['status'].return_value = dict.fromkeys(
            ('active_task_count', 'running_task_count', 'resource_wait_task_count'), 0)
        mocks['run'].return_value = SimpleNamespace(stdout='fixture passed')
        mocks['service_state'].return_value = (True, 123)
        stack.enter_context(redirect_stdout(io.StringIO()))
        yield mocks


class WorkerRuntimeTest(unittest.TestCase):
    def test_preflight_failure_restores_files_without_stopping_service(self):
        module = load('install-worker-runtime')
        for role in ('engineer', 'reviewer'):
            with self.subTest(role=role), installer_mocks(module) as mocks:
                def execute(argv):
                    if '--preflight-' + role in argv:
                        self.assertEqual(argv[0:2], ['/usr/bin/python3', '-I'])
                        raise subprocess.CalledProcessError(1, 'fixture')
                    return SimpleNamespace(stdout='fixture passed')
                mocks['run'].side_effect = execute
                with self.assertRaises(subprocess.CalledProcessError):
                    module.install(HERE, {'pid': 1}, {})
                mocks['restore'].assert_called_once_with(Path('/safe/backup'))
                mocks['bootout'].assert_not_called()
                mocks['bootstrap'].assert_not_called()

    def test_failed_restart_restores_and_restarts_original_service(self):
        module = load('install-worker-runtime')
        with installer_mocks(module) as mocks:
            mocks['wait_running'].side_effect = RuntimeError('unhealthy')
            with self.assertRaisesRegex(RuntimeError, 'unhealthy'):
                module.install(HERE, {'pid': 1}, {})
            self.assertEqual(mocks['bootout'].call_count, 2)
            mocks['restore'].assert_called_once_with(Path('/safe/backup'))
            self.assertEqual(mocks['bootstrap'].call_count, 2)

    def test_recovery_shutdown_timeout_still_restores_files_without_bootstrap(self):
        module = load('install-worker-runtime')
        with installer_mocks(module) as mocks:
            mocks['bootout'].side_effect = subprocess.TimeoutExpired('launchctl', 40)
            with self.assertRaisesRegex(RuntimeError, 'Original files restored'):
                module.install(HERE, {'pid': 1}, {})
            mocks['restore'].assert_called_once_with(Path('/safe/backup'))
            mocks['bootstrap'].assert_not_called()

    def test_manual_rollback_accepts_only_idle_or_verified_stopped_worker(self):
        module = load('install-worker-runtime')
        with patch.object(module, 'service_state', return_value=(False, None)):
            self.assertFalse(module.rollback_allowed({'status': 'stopped'}))
        with patch.object(module, 'service_state', return_value=(True, None)):
            self.assertTrue(module.rollback_allowed({'status': 'stopped'}))
        with patch.object(module, 'service_state', return_value=(True, 123)):
            with self.assertRaises(RuntimeError):
                module.rollback_allowed({'status': 'stopped'})
            with self.assertRaises(RuntimeError):
                module.rollback_allowed({'status': 'unknown'})
            with self.assertRaises(RuntimeError):
                module.rollback_allowed({'status': 'running', 'active_task_count': 1})

    def test_service_absence_is_distinct_from_other_launchctl_errors(self):
        module = load('install-worker-runtime')
        missing = subprocess.CalledProcessError(113, 'print', stderr='Could not find service')
        with patch.object(module, 'run', side_effect=missing):
            self.assertEqual(module.service_state(), (False, None))
        with patch.object(module, 'run', side_effect=subprocess.TimeoutExpired('print', 40)):
            with self.assertRaises(subprocess.TimeoutExpired):
                module.service_state()

    def test_atomic_write_rejects_symlink_target(self):
        module = load('install-worker-runtime')
        with tempfile.TemporaryDirectory() as temp:
            target = Path(temp) / 'target'
            target.symlink_to(Path(temp) / 'elsewhere')
            with self.assertRaisesRegex(RuntimeError, 'symlink'):
                module.atomic(target, b'content', 0o755, 0, 0)

    def test_installer_preserves_service_settings_but_enters_worker_session(self):
        module = load('install-worker-runtime')
        launch = load('worker-launch')
        original = {'Label': module.LABEL, 'ProgramArguments': launch.WORKER_ARGS,
                    'UserName': 'webworker', 'GroupName': 'webworker',
                    'KeepAlive': True, 'EnvironmentVariables': {'HOME': str(module.ROOT)}}
        updated = module.configuration(original)
        self.assertNotIn('UserName', updated)
        self.assertNotIn('GroupName', updated)
        self.assertEqual(updated['ProgramArguments'], ['/usr/bin/python3', '-I', str(module.ENTRY)])
        self.assertTrue(updated['KeepAlive'])
        self.assertEqual(updated['EnvironmentVariables']['MULTICA_REPO_CHECKOUT_MODE'], 'isolated')
        self.assertEqual(original['UserName'], 'webworker')
        self.assertEqual(module.configuration(updated), updated)

    def test_installer_rejects_busy_or_incomplete_status(self):
        module = load('install-worker-runtime')
        idle = dict.fromkeys(('active_task_count', 'running_task_count', 'resource_wait_task_count'), 0)
        module.require_idle(idle)
        for key in idle:
            with self.assertRaises(RuntimeError):
                module.require_idle(dict(idle, **{key: 1}))
        with self.assertRaises(RuntimeError):
            module.require_idle({})

    def test_installer_refuses_unknown_service(self):
        module = load('install-worker-runtime')
        with self.assertRaisesRegex(RuntimeError, 'Unexpected LaunchDaemon'):
            module.configuration({'Label': module.LABEL, 'ProgramArguments': ['/fake/worker']})

    def test_task_shell_keeps_isolated_checkout_mode_and_role_boundaries(self):
        module = load('codex-role')
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            workdir = root / 'tasks/task-a/workdir'
            workdir.mkdir(parents=True)
            task_home = workdir.parent / 'codex-home'
            task_home.mkdir()
            config = task_home / 'config.toml'
            original = "[shell_environment_policy]\ninherit = 'all'\ninclude_only = ['HOME', 'PATH', 'MULTICA_TASK_ID']\n"
            config.write_text(original)
            for role in ('engineer', 'reviewer'):
                (root / 'runtime' / role).mkdir(parents=True)
            with patch.object(module, 'ROOT', root), patch.object(module.Path, 'cwd', return_value=workdir), patch.dict(os.environ, {'CODEX_HOME': str(task_home)}, clear=True), patch.object(module.sys, 'argv', ['codex-engineer', '--exec', '/fake/node']), patch.object(module.os, 'execve') as execute:
                module.main()
                text = config.read_text()
                self.assertIn("'MULTICA_REPO_CHECKOUT_MODE'", text)
                self.assertEqual(text.count('MULTICA_REPO_CHECKOUT_MODE'), 1)
                self.assertIn("'MULTICA_TASK_ID'", text)
                self.assertEqual(execute.call_args.args[2]['MULTICA_REPO_CHECKOUT_MODE'], 'isolated')
                policy = execute.call_args.args[1][2]
                self.assertIn('(deny file-read* (subpath "/Users")', policy)
                self.assertNotIn('(subpath ' + json.dumps(str(root / 'tasks')) + ')', policy)
                self.assertEqual((task_home / 'auth.json').resolve(), root / 'runtime/engineer/auth.json')
                module.main()
                self.assertEqual(config.read_text(), text, 'shell-policy repair must be idempotent')

    def test_role_launcher_rejects_task_home_outside_its_task(self):
        module = load('codex-role')
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp).resolve()
            workdir = root / 'tasks/task-a/workdir'
            workdir.mkdir(parents=True)
            with patch.object(module, 'ROOT', root), patch.object(module.Path, 'cwd', return_value=workdir), patch.dict(os.environ, {'CODEX_HOME': str(root / 'tasks/task-b/codex-home')}, clear=True), patch.object(module.sys, 'argv', ['codex-engineer']), patch.object(module.os, 'execve') as execute:
                with self.assertRaisesRegex(SystemExit, 'Unexpected task Codex home'):
                    module.main()
                execute.assert_not_called()

    def test_browser_context_drops_privileges_before_worker_owned_executable(self):
        module = load('worker-launch')
        argv = module.command(600, module.WORKER_ARGS)
        self.assertEqual(argv[:8], ['/bin/launchctl', 'asuser', '600', '/usr/bin/sudo', '-H', '-u', 'webworker', '--'])
        self.assertGreater(argv.index('/Users/webworker/bin/multica'), argv.index('--'))
        self.assertIn('MULTICA_REPO_CHECKOUT_MODE=isolated', argv)

    def test_root_entrypoint_rejects_non_root(self):
        module = load('worker-launch')
        with patch.object(module.os, 'geteuid', return_value=600), patch.object(module.os, 'execv') as execute:
            with self.assertRaisesRegex(SystemExit, 'must run as root'):
                module.main()
            execute.assert_not_called()


if __name__ == '__main__':
    unittest.main()
