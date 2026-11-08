import run  # Apply Locust/gevent patches before importing networking modules.

import contextlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import io
import json
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
import unittest

class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_):
        pass

    def handle(self):
        try:
            super().handle()
        except ConnectionResetError:
            # The client deliberately drops connections after invalid responses.
            pass

    def do_POST(self):
        self.respond("POST")

    def do_GET(self):
        self.respond("GET")

    def respond(self, method):
        with self.server.lock:
            self.server.calls.append((method, self.path))
            call = len(self.server.calls)
        if method == "POST":
            original = json.loads(self.rfile.read(int(self.headers["Content-Length"])))['url']
            index = original.rsplit("/", 1)[-1]
            payload = {"short_url": f"https://public.invalid/other/links/C{index}"}
        else:
            index = self.path.rsplit("/", 1)[-1][1:]
            payload = {"url": f"https://example.com/shortener-loadtest/v1/{index}"}
        status = 200
        body = json.dumps(payload).encode()
        if call > self.server.fail_after:
            mode = self.server.mode
            if mode == "status":
                status = 503
            elif mode == "json":
                body = b"not json"
            elif mode == "wrong":
                body = b'{}'
            elif mode == "large":
                body = b"x" * (run.MAX_RESPONSE + 1)
            elif mode == "slow":
                time.sleep(0.1)
            elif mode == "changed" and method == "POST":
                body = b'{"short_url":"https://public.invalid/links/Different"}'
        try:
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass


@contextlib.contextmanager
def server(mode="ok", fail_after=0):
    instance = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    instance.mode = mode
    instance.fail_after = fail_after
    instance.calls = []
    instance.lock = threading.Lock()
    thread = threading.Thread(target=instance.serve_forever)
    thread.start()
    try:
        yield instance
    finally:
        instance.shutdown()
        instance.server_close()
        thread.join()


def config(instance, *extra):
    return run.arguments([
        "--base-url", f"http://127.0.0.1:{instance.server_port}/api",
        "--dataset-size", "2", "--concurrency", "4",
        "--duration", "5", "--max-requests", "100", *extra,
    ])


class LoadTest(unittest.TestCase):
    def test_profile_prefix_public_host_and_reuse(self):
        with server() as instance:
            options = config(instance)
            for _ in range(2):
                result = run.experiment(options, threading.Event())
                self.assertTrue(result.passed)
                self.assertEqual(result.prepared, 2)
                self.assertEqual(result.total, 100)
                self.assertEqual(len(result.samples["GET"]), 95)
                self.assertEqual(len(result.samples["POST"]), 5)
                self.assertEqual(result.reason, "max_requests")
            self.assertTrue(all(path.startswith("/api/links") for _, path in instance.calls))
            self.assertEqual(len(instance.calls), 208)

    def test_request_failures_continue(self):
        for mode, expected in [("status", "HTTP_503"), ("json", "invalid_JSON"),
                               ("wrong", "invalid_response"), ("large", "response_too_large"),
                               ("slow", "timeout")]:
            with self.subTest(mode=mode), server(mode, fail_after=4) as instance:
                result = run.experiment(config(instance, "--max-requests", "8", "--timeout", "0.03"),
                                        threading.Event())
                self.assertFalse(result.passed)
                self.assertEqual(result.total, 8)
                self.assertEqual(result.failures, 8)
                self.assertEqual(result.errors["GET"][expected], 8)

    def test_preparation_failure_and_report(self):
        with server("status") as instance, tempfile.TemporaryDirectory() as directory:
            options = config(instance)
            result = run.experiment(options, threading.Event())
            self.assertEqual(result.reason, "preparation_failed: HTTP_503")
            self.assertEqual(result.total, 0)
            path = run.save_report(options, result, Path(directory))
            self.assertIn("FAIL", path.read_text())

    def test_changed_post_code_fails(self):
        with server("changed", fail_after=4) as instance:
            result = run.experiment(config(instance, "--max-requests", "20"), threading.Event())
            self.assertFalse(result.passed)
            self.assertEqual(result.total, 20)
            self.assertEqual(result.failures, 1)
            self.assertEqual(result.errors["POST"]["changed_code"], 1)

    def test_duration_and_interruption(self):
        with server() as instance:
            options = config(instance, "--duration", "0.1", "--max-requests", "100000")
            result = run.experiment(options, threading.Event())
            self.assertTrue(result.passed)
            self.assertEqual(result.reason, "duration")
            self.assertGreaterEqual(result.elapsed, 0.1)
            stop = threading.Event()
            timer = threading.Timer(0.2, stop.set)
            timer.start()
            try:
                result = run.experiment(config(instance, "--max-requests", "100000"), stop)
            finally:
                timer.join()
            self.assertFalse(result.passed)
            self.assertEqual(result.reason, "interrupted")
            self.assertGreater(result.total, 0)

    def test_reports_history_latest_and_metrics(self):
        with server() as instance, tempfile.TemporaryDirectory() as directory:
            options = config(instance)
            result = run.experiment(options, threading.Event())
            first = run.save_report(options, result, Path(directory))
            second = run.save_report(options, result, Path(directory))
            self.assertNotEqual(first, second)
            self.assertEqual(first.read_bytes(), second.read_bytes())
            self.assertEqual(second.read_bytes(), (Path(directory) / "latest.md").read_bytes())
            self.assertEqual(len(list(Path(directory).glob("*.md"))), 3)
            self.assertFalse(list(Path(directory).glob("*.tmp")))
            text = second.read_text()
            self.assertIn("| GET | успех | 95 |", text)
            self.assertIn("| POST | успех | 5 |", text)
            self.assertNotIn("public.invalid", text)
            self.assertNotIn("example.com", text)
        samples = [(0.001, True), (0.003, True), (0.010, False)]
        self.assertIn("2.000 | 1.000 | 3.000 | 3.000 | 3.000", run.latency_row("GET", True, samples))

    def test_invalid_arguments(self):
        for args in [["--duration", "nan"], ["--duration", "inf"], ["--timeout", "0"],
                     ["--concurrency", "-1"], ["--dataset-size", "0"],
                     ["--max-requests", "0"], ["--base-url", "ftp://example.com"],
                     ["--base-url", "https://user:secret@example.com"],
                     ["--base-url", "http://example.com:bad"],
                     ["--base-url", "http://example.com/?token=secret"]]:
            with self.subTest(args=args), contextlib.redirect_stderr(io.StringIO()):
                with self.assertRaises(SystemExit) as error:
                    run.arguments(["--base-url", "http://localhost:8080", *args])
                self.assertEqual(error.exception.code, 2)

    def test_cli_ctrl_c_saves_report(self):
        # Copy only the standalone script so subprocess reports stay in a temporary directory.
        with server() as instance, tempfile.TemporaryDirectory() as directory:
            script = Path(directory) / "run.py"
            script.write_bytes(Path(run.__file__).read_bytes())
            process = subprocess.Popen([
                sys.executable, str(script), "--base-url",
                f"http://127.0.0.1:{instance.server_port}",
                "--dataset-size", "1", "--duration", "30",
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            try:
                deadline = time.monotonic() + 5
                while time.monotonic() < deadline:
                    with instance.lock:
                        if len(instance.calls) > 5:
                            break
                    time.sleep(0.01)
                process.send_signal(signal.SIGINT)
                stdout, stderr = process.communicate(timeout=5)
                self.assertEqual(process.returncode, 130, stderr)
                self.assertIn("FAIL", stdout)
                self.assertIn("interrupted", (Path(directory) / "reports/latest.md").read_text())
            finally:
                if process.poll() is None:
                    process.kill()
                    process.communicate()


if __name__ == "__main__":
    unittest.main()
