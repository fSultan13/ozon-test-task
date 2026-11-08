"""Regression checks for release activation and failure recovery, using a fake Docker CLI."""

import fcntl
import hashlib
import io
import json
import os
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path


REPO = Path(__file__).resolve().parents[2]
RELEASE = "a" * 40 + "-123-1"
FAKE_DOCKER = """#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
args = sys.argv[1:]
with open(os.environ['DOCKER_TRACE'], 'a') as trace:
    trace.write(json.dumps(args) + '\\n')
mode = os.environ.get('FAIL_AT', '')
if 'pull' in args and mode == 'pull':
    sys.exit(1)
if 'psql' in args:
    print(100)
if 'pg_dump' in args:
    if mode == 'backup':
        sys.exit(1)
    print('fake-database-backup')
if 'up' in args and '--no-deps' not in args and '/releases/' in args[args.index('-f') + 1] and mode == 'up':
    sys.exit(1)
if '--format' in args:
    print(json.dumps({'services': {key: {'environment': {'PUBLIC_BASE_URL': 'http://example.com'}} for key in ['api-memory', 'api-postgres']}}))
"""


class DeploymentTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)
        self.root = self.work / "server"
        (self.root / "deploy/demo").mkdir(parents=True)
        self.secret = self.root / "deploy/demo/.env"
        self.secret.write_text("RELEASE=bootstrap\nPOSTGRES_PASSWORD=keep-this-secret\n")
        for name in ("compose.yml", "images.env"):
            (self.root / "deploy/demo" / name).write_bytes((REPO / "deploy/demo" / name).read_bytes())
        self.bundle = self.work / "bundle"
        self.bundle.mkdir()
        self.trace = self.work / "docker.jsonl"
        binaries = self.work / "bin"
        binaries.mkdir()
        docker = binaries / "docker"
        docker.write_text(FAKE_DOCKER)
        docker.chmod(0o755)
        if shutil.which("flock") is None:
            # macOS has the same locking syscall but no util-linux CLI.
            flock = binaries / "flock"
            flock.write_text("#!/usr/bin/env python3\nimport fcntl, sys\ntry:\n    fcntl.flock(int(sys.argv[2]), fcntl.LOCK_EX | fcntl.LOCK_NB)\nexcept BlockingIOError:\n    sys.exit(1)\n")
            flock.chmod(0o755)
        self.env = dict(os.environ, DEPLOY_ROOT=str(self.root), DOCKER_TRACE=str(self.trace),
                        PATH=str(binaries) + os.pathsep + os.environ["PATH"])
        self.make_bundle()

    def make_bundle(self, smoke_failure=False, invalid_path=False):
        (self.bundle / "release.env").write_text(
            f"RELEASE={RELEASE}\n"
            + "API_IMAGE=fsultan13/ozon-url-shortener@sha256:" + "b" * 64 + "\n"
            + "GATEWAY_IMAGE=fsultan13/ozon-url-shortener-gateway@sha256:" + "c" * 64 + "\n")
        with tarfile.open(self.bundle / "config.tar.gz", "w:gz") as archive:
            for name in ("compose.yml", "images.env"):
                archive.add(REPO / "deploy/demo" / name, arcname="deploy/demo/" + name)
            smoke = b"raise RuntimeError('smoke failed')\n" if smoke_failure else b"print('smoke passed')\n"
            info = tarfile.TarInfo("scripts/check-demo.py")
            info.size = len(smoke)
            archive.addfile(info, io.BytesIO(smoke))
            if invalid_path:
                info = tarfile.TarInfo("../../escape")
                info.size = 1
                archive.addfile(info, io.BytesIO(b"x"))
        checksums = []
        for name in ("release.env", "config.tar.gz"):
            digest = hashlib.sha256((self.bundle / name).read_bytes()).hexdigest()
            checksums.append(f"{digest}  {name}")
        (self.bundle / "SHA256SUMS").write_text("\n".join(checksums) + "\n")

    def run_deploy(self, fail_at=""):
        result = subprocess.run(["bash", str(REPO / "deploy/demo/deploy.sh"), RELEASE, str(self.bundle)],
                                env=dict(self.env, FAIL_AT=fail_at), capture_output=True, text=True)
        self.assertEqual(self.secret.read_text(), "RELEASE=bootstrap\nPOSTGRES_PASSWORD=keep-this-secret\n")
        self.assertNotIn("keep-this-secret", result.stdout + result.stderr)
        return result

    def commands(self):
        return [json.loads(line) for line in self.trace.read_text().splitlines()] if self.trace.exists() else []

    def test_success_keeps_secret_and_records_previous_release_and_backup(self):
        result = self.run_deploy()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        target = self.root / "releases" / RELEASE
        self.assertEqual((self.root / "current-release").read_text().strip(), str(target))
        self.assertEqual((self.root / "previous-release").read_text().strip(), str(self.root))
        self.assertTrue((self.root / "backups" / (RELEASE + ".dump")).is_file())

    def test_pull_and_backup_failure_leave_application_untouched(self):
        for failure in ("pull", "backup"):
            with self.subTest(failure=failure):
                # Each deployment attempt has its own incoming release directory.
                target = self.root / "releases" / RELEASE
                if target.exists():
                    shutil.rmtree(target)
                self.trace.write_text("")
                result = self.run_deploy(failure)
                self.assertNotEqual(result.returncode, 0)
                self.assertFalse((self.root / "current-release").exists())
                self.assertFalse(any("up" in args for args in self.commands()))
                self.assertFalse((self.root / "backups" / (RELEASE + ".dump")).exists())

    def test_start_failure_restores_only_previous_application(self):
        result = self.run_deploy("up")
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.root / "current-release").exists())
        rollback = [args for args in self.commands() if "up" in args and "--no-deps" in args]
        self.assertEqual(len(rollback), 1)
        self.assertEqual(rollback[0][-3:], ["api-memory", "api-postgres", "gateway"])
        self.assertEqual(rollback[0][rollback[0].index("-f") + 1], str(self.root / "deploy/demo/compose.yml"))

    def test_smoke_failure_rolls_back_and_keeps_current_marker(self):
        self.make_bundle(smoke_failure=True)
        (self.root / "current-release").write_text(str(self.root) + "\n")
        result = self.run_deploy()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / "current-release").read_text().strip(), str(self.root))
        self.assertTrue(any("--no-deps" in args for args in self.commands()))

    def test_rollback_uses_active_release_manifest_after_first_deployment(self):
        previous = self.root / "releases" / ("d" * 40 + "-99-1")
        (previous / "deploy/demo").mkdir(parents=True)
        for name in ("compose.yml", "images.env"):
            shutil.copyfile(REPO / "deploy/demo" / name, previous / "deploy/demo" / name)
        (previous / "release.env").write_text("RELEASE=previous\n")
        (self.root / "current-release").write_text(str(previous) + "\n")
        result = self.run_deploy("up")
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((self.root / "current-release").read_text().strip(), str(previous))
        rollback = next(args for args in self.commands() if "--no-deps" in args)
        self.assertIn(str(previous / "release.env"), rollback)
        self.assertIn(str(self.secret), rollback)
        self.assertEqual(rollback[rollback.index("-f") + 1], str(previous / "deploy/demo/compose.yml"))

    def test_bad_checksum_and_archive_path_are_rejected_before_docker(self):
        (self.bundle / "release.env").write_text("tampered")
        self.assertNotEqual(self.run_deploy().returncode, 0)
        self.assertFalse(self.commands())
        self.make_bundle(invalid_path=True)
        self.assertNotEqual(self.run_deploy().returncode, 0)
        self.assertFalse(self.commands())
        self.assertFalse((self.work / "escape").exists())

    def test_server_lock_prevents_overlapping_deployments(self):
        with (self.root / ".deploy.lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = self.run_deploy()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse(self.commands())


if __name__ == "__main__":
    unittest.main()
