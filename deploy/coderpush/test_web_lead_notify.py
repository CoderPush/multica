"""Exercise the real queue helper against a fake Multica CLI, without sending."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest


class QueueParentTest(unittest.TestCase):
    def test_direct_and_comment_triggered_queue(self):
        helper = Path(__file__).with_name('web-lead-notify')
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / 'body.md').write_text('Verified release update.')
            cli = root / 'multica'
            cli.write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
if sys.argv[1:3] == ['issue', 'get']:
 print(json.dumps({'id':'issue-1','identifier':'CLP-48','status':'done','project_id':'1f065765-5630-4e67-9f16-adffaa6b3ce3','workspace_id':'7aec4dc4-be92-4dab-9eb9-2ab405af1e7c'}))
else:
 Path(os.environ['CAPTURE']).write_text(json.dumps({'args':sys.argv[1:],'body':sys.stdin.read()}))
 if os.environ.get('REQUIRE_PARENT') and '--parent' not in sys.argv: sys.exit(1)
 print(json.dumps({'id':'queued-1'}))
''')
            cli.chmod(0o755)
            env = dict(os.environ, PATH=f"{tmp}:{os.environ['PATH']}",
                       MULTICA_AGENT_ID='23ac88c6-c40c-4800-ace7-fb22ed69f7bb',
                       MULTICA_WORKSPACE_ID='7aec4dc4-be92-4dab-9eb9-2ab405af1e7c',
                       CAPTURE=str(root / 'capture.json'))
            command = [sys.executable, str(helper), 'CLP-48', '--reply-to', 'om_abcdefgh',
                       '--milestone', 'shipped', '--body-file', str(root / 'body.md')]
            result = subprocess.run(command, env=env, capture_output=True, text=True, check=True)
            self.assertEqual(json.loads(result.stdout)['state'], 'queued')
            self.assertNotIn('--parent', json.loads((root / 'capture.json').read_text())['args'])
            for blank in ['', '   ']:
                rejected = subprocess.run(command + ['--parent', blank], env=env, capture_output=True, text=True)
                self.assertNotEqual(rejected.returncode, 0)
                self.assertIn('non-empty', rejected.stderr)
            env['REQUIRE_PARENT'] = '1'
            rejected = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertNotEqual(rejected.returncode, 0)
            self.assertIn('Reconcile', rejected.stdout)
            result = subprocess.run(command + ['--parent', 'trigger-1'], env=env,
                                    capture_output=True, text=True, check=True)
            queued = json.loads((root / 'capture.json').read_text())
            self.assertEqual(queued['args'][-2:], ['--parent', 'trigger-1'])
            self.assertEqual(json.loads(queued['body'].split('\n', 1)[1])['reply_to'], 'om_abcdefgh')
            self.assertEqual(json.loads(result.stdout)['state'], 'queued')


if __name__ == '__main__':
    unittest.main()
