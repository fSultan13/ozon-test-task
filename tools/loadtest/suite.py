#!/usr/bin/env python3
"""Run the agreed sequential VPS step and endurance profile."""
import run  # Apply gevent patches first.

import argparse
from datetime import datetime, timezone
import json
import math
from pathlib import Path
import resource
import signal
import threading
import time


STAGES = [("step-10", 10, 120), ("step-25", 25, 120),
          ("step-50", 50, 120), ("step-100", 100, 120), ("endurance-50", 50, 600)]


def metrics(samples, elapsed):
    values = sorted(duration * 1000 for duration, _ in samples)
    errors = sum(not success for _, success in samples)
    return {
        "requests": len(samples), "errors": errors,
        "error_percent": 100 * errors / len(samples) if samples else 0,
        "rps": len(samples) / elapsed if elapsed else 0,
        **{f"p{int(q * 100)}_ms": values[math.ceil(len(values) * q) - 1] if values else None
           for q in (0.5, 0.95, 0.99)},
    }


def threshold(stats):
    if stats["requests"] and stats["error_percent"] >= 1:
        return "error_percent>=1"
    if stats["p95_ms"] is not None and stats["p95_ms"] >= 1000:
        return "p95>=1000ms"
    return None


class Monitor:
    def __init__(self, stop):
        self.stop = stop
        self.last = None
        self.offsets = {"GET": 0, "POST": 0}
        self.windows = []
        self.breach = None

    def __call__(self, result, final=False):
        now = time.monotonic()
        if self.last is None:
            self.last = now
        elapsed = now - self.last
        if not final and elapsed < 10:
            return
        samples = []
        for method, values in result.samples.items():
            samples.extend(values[self.offsets[method]:])
            self.offsets[method] = len(values)
        if samples:
            stats = metrics(samples, elapsed)
            stats["seconds"] = elapsed
            self.windows.append(stats)
            self.breach = self.breach or threshold(stats)
            print(f"  window: {stats['requests']} requests, {stats['rps']:.1f} RPS, "
                  f"p95={stats['p95_ms']:.1f}ms, errors={stats['error_percent']:.2f}%", flush=True)
        self.last = now
        if self.breach:
            self.stop.set()


def summary(directory, rows):
    lines = ["# VPS: ступенчатый и длительный нагрузочный тест", "",
             "Профиль: 10 → 25 → 50 → 100 клиентов по 120 с, затем 50 клиентов в течение 600 с.",
             "Стенды проверяются последовательно; между этапами — 10 с без нагрузки.",
             "95% GET / 5% повторных POST, 100 переиспользуемых ссылок, I/O timeout 3 с.",
             "Порог остановки: ≥1% ошибок или p95 ≥1000 мс в окне завершённых запросов (~10 с).",
             "При нарушении порога оставшиеся этапы этого стенда пропускаются. Любая ошибка даёт FAIL.", "",
             "| Стенд | Этап | Секунды | Запросы | RPS | p50, мс | p95, мс | p99, мс | Ошибки | Итог |",
             "|---|---|---:|---:|---:|---:|---:|---:|---:|---|"]
    for row in rows:
        if row.get("skipped"):
            lines.append(f"| {row['target']} | {row['stage']} | — | — | — | — | — | — | — | SKIP |")
            continue
        m = row["metrics"]
        pct = [f"{m[key]:.2f}" if m[key] is not None else "—" for key in ("p50_ms", "p95_ms", "p99_ms")]
        lines.append(f"| {row['target']} | [{row['stage']}]({row['file']}) | {row['elapsed']:.2f} | "
                     f"{m['requests']} | {m['rps']:.2f} | {' | '.join(pct)} | {m['errors']} | {row['status']} |")
    lines.extend(["", "Перцентили в сводке включают все исходы; в отчётах этапов они разделены по методу и исходу.",
                  "Измерение включает сеть, gateway и Locust-клиент, закрытая модель без целевого RPS.",
                  "Это не доказательство максимальной мощности сервиса или числа поддерживаемых пользователей.",
                  "Создание новых URL, заполнение хранилища, рестарты и восстановление БД не проверяются.",
                  "CPU/RAM сервера не измерялись; лимиты из deploy/demo/compose.yml не подтверждают настройки работающего VPS.",
                  "JSON сохраняет интервалы, причины остановки и CPU-время клиента (включая подготовку); тела ответов и ссылки исключены.", ""])
    (directory / "summary.md").write_text("\n".join(lines), encoding="utf-8")
    (directory / "results.json").write_text(json.dumps(rows, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--memory-url", required=True, type=run.base_url)
    parser.add_argument("--postgres-url", required=True, type=run.base_url)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    # Refuse to overwrite an earlier suite.
    args.output_dir.mkdir(parents=True, exist_ok=False)
    interrupted = threading.Event()
    previous = signal.signal(signal.SIGINT, lambda *_: interrupted.set())
    rows = []
    try:
        for target, url in (("memory", args.memory_url), ("postgres", args.postgres_url)):
            blocked = False
            for stage, users, duration in STAGES:
                if blocked or interrupted.is_set():
                    rows.append({"target": target, "stage": stage, "skipped": True})
                    summary(args.output_dir, rows)
                    continue
                print(f"START {target} {stage}: {users} clients, {duration}s", flush=True)
                config = run.arguments(["--base-url", url, "--label", f"vps-{target}-{stage}",
                                        "--duration", str(duration), "--concurrency", str(users),
                                        "--max-requests", "2000000"])
                stop = threading.Event()
                monitor = Monitor(stop)

                def observe(result):
                    if interrupted.is_set():
                        stop.set()
                    monitor(result)

                cpu_before = time.process_time()
                result = run.experiment(config, stop, observe)
                monitor(result, final=True)
                if monitor.breach:
                    result.reason = "threshold: " + monitor.breach
                all_samples = [sample for values in result.samples.values() for sample in values]
                stats = metrics(all_samples, result.elapsed)
                blocked = not result.passed
                filename = f"{target}-{stage}.md"
                content = run.markdown(config, result).replace(
                    "Порогов RPS и задержки нет; любая ошибка означает FAIL.",
                    "Порог остановки серии: ≥1% ошибок или p95 ≥1000 мс в окне ~10 с; любая ошибка означает FAIL.")
                content = content.replace("# Нагрузочный тест URL Shortener", f"# VPS {target}: {stage}", 1)
                (args.output_dir / filename).write_text(content, encoding="utf-8")
                rows.append({"target": target, "stage": stage, "file": filename,
                             "started": result.started, "finished": datetime.now(timezone.utc).isoformat(),
                             "elapsed": result.elapsed, "cpu_seconds": time.process_time() - cpu_before,
                             "client_peak_rss_bytes": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
                             * (1 if run.sys.platform == "darwin" else 1024),
                             "reason": result.reason, "status": "PASS" if result.passed else "FAIL",
                             "metrics": stats, "errors": result.errors, "windows": monitor.windows})
                summary(args.output_dir, rows)
                print(f"END {target} {stage}: {rows[-1]['status']}, {stats['rps']:.1f} RPS", flush=True)
                del all_samples, result
                until = time.monotonic() + 10
                while time.monotonic() < until and not interrupted.is_set():
                    run.gevent.sleep(0.1)
    finally:
        signal.signal(signal.SIGINT, previous)
        summary(args.output_dir, rows)
    return 130 if interrupted.is_set() else int(any(row.get("status") == "FAIL" for row in rows))


if __name__ == "__main__":
    raise SystemExit(main())
