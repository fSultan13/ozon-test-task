"""Check PostgreSQL startup gating without running actual containers."""

import json
import os
import subprocess
import tempfile
import unittest
from pathlib import Path


REPO = Path(__file__).resolve().parents[2]
DOCKER = """#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
args = sys.argv[1:]
with open(os.environ['TRACE'], 'a') as trace:
    trace.write(json.dumps(['docker'] + args) + '\\n')
state = Path(os.environ['STATE'])
attempts = int(state.read_text()) if state.exists() else 0
if 'pg_isready' in args:
    # The temporary init server accepts Unix connections before final startup.
    if '-h' not in args:
        sys.exit(0)
    assert args[args.index('-h') + 1] == '127.0.0.1'
    attempts += 1
    state.write_text(str(attempts))
    sys.exit(0 if attempts >= 3 and not os.environ.get('NEVER_READY') else 1)
if 'psql' in args:
    sys.stdin.read()
    if attempts < 3:
        print('PostgreSQL init server socket disappeared', file=sys.stderr)
        sys.exit(2)
if args[0] == 'port':
    print('127.0.0.1:15432')
"""


class PostgresStartupTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name)
        self.trace = self.work / 'trace.jsonl'
        self.state = self.work / 'state'
        self.write_executable('docker', DOCKER)
        self.write_executable('sleep', '#!/bin/sh\nexit 0\n')
        self.write_executable('go', """#!/usr/bin/env python3
import json, os, sys
with open(os.environ['TRACE'], 'a') as trace:
    trace.write(json.dumps(['go'] + sys.argv[1:]) + '\\n')
assert os.environ['TEST_DATABASE_URL'] == 'postgres://postgres:test@127.0.0.1:15432/postgres?sslmode=disable'
""")

    def write_executable(self, name, source):
        path = self.work / name
        path.write_text(source)
        path.chmod(0o755)

    def run_script(self, never_ready=False):
        env = dict(os.environ, DOCKER=str(self.work / 'docker'), TRACE=str(self.trace), STATE=str(self.state),
                   PATH=str(self.work) + os.pathsep + os.environ['PATH'])
        env.pop('NEVER_READY', None)
        if never_ready:
            env['NEVER_READY'] = '1'
        result = subprocess.run(['sh', 'scripts/test-postgres.sh'], cwd=REPO, env=env,
                                capture_output=True, text=True, timeout=15)
        commands = [json.loads(line) for line in self.trace.read_text().splitlines()]
        return result, commands

    def test_temporary_unix_server_does_not_start_migrations_early(self):
        result, commands = self.run_script()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(int(self.state.read_text()), 3)
        self.assertEqual(sum('psql' in command for command in commands), 3)
        self.assertTrue(any(command[0] == 'go' for command in commands))
        self.assertEqual(commands[-1][1:3], ['rm', '-f'])

    def test_readiness_timeout_skips_migrations_and_cleans_container(self):
        result, commands = self.run_script(never_ready=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('PostgreSQL did not start', result.stderr)
        self.assertEqual(int(self.state.read_text()), 30)
        self.assertFalse(any('psql' in command or command[0] == 'go' for command in commands))
        self.assertEqual(commands[-1][1:3], ['rm', '-f'])


if __name__ == '__main__':
    unittest.main()
