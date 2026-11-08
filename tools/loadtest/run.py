#!/usr/bin/env python3
"""Bounded Locust load test for the shortener; see requirements.txt."""

# Patch before importing networking/threading modules, as required by Locust.
from gevent import monkey
monkey.patch_all()

import gevent
from locust import HttpUser, __version__ as LOCUST_VERSION, task
from locust.env import Environment
from locust.exception import StopUser
import requests

import argparse
from collections import Counter
from dataclasses import dataclass, field
from datetime import datetime, timezone
import json
import math
from pathlib import Path
import re
import signal
import sys
import threading
import time
from urllib.parse import urlsplit
import uuid


MAX_RESPONSE = 65536
REPORTS = Path(__file__).resolve().parent / "reports"


def positive_float(value):
    number = float(value)
    if not math.isfinite(number) or number <= 0:
        raise argparse.ArgumentTypeError("must be finite and positive")
    return number


def positive_int(value):
    number = int(value)
    if number <= 0:
        raise argparse.ArgumentTypeError("must be positive")
    return number


def base_url(value):
    try:
        parsed = urlsplit(value)
        if (parsed.scheme not in ("http", "https") or not parsed.hostname
                or parsed.username is not None or parsed.password is not None
                or "?" in value or "#" in value
                or any(ord(char) <= 32 or ord(char) == 127 for char in value)):
            raise ValueError
        _ = parsed.port
        value.encode("ascii")
    except (ValueError, UnicodeError):
        raise argparse.ArgumentTypeError(
            "expected an ASCII HTTP(S) URL without credentials, query or fragment"
        ) from None
    return value.rstrip("/")


def arguments(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", required=True, type=base_url)
    parser.add_argument("--duration", type=positive_float, default=30)
    parser.add_argument("--concurrency", type=positive_int, default=10)
    parser.add_argument("--timeout", type=positive_float, default=3)
    parser.add_argument("--dataset-size", type=positive_int, default=100)
    parser.add_argument("--max-requests", type=positive_int, default=100000)
    parser.add_argument("--label", default="unspecified")
    return parser.parse_args(argv)


class RequestFailure(Exception):
    """Safe category, without response bodies or URLs."""


def short_code(payload):
    if not isinstance(payload, dict) or not isinstance(payload.get("short_url"), str):
        raise RequestFailure("invalid_response")
    try:
        parsed = urlsplit(payload["short_url"])
        code = parsed.path.rsplit("/", 1)[-1]
        if (parsed.scheme not in ("http", "https") or not parsed.hostname
                or parsed.query or parsed.fragment
                or not parsed.path.endswith("/links/" + code)
                or not re.fullmatch(r"[a-zA-Z0-9_]{1,10}", code)):
            raise ValueError
    except ValueError:
        raise RequestFailure("invalid_response") from None
    return code


class Client:
    def __init__(self, config, session=None):
        self.base = config.base_url
        self.timeout = config.timeout
        self.measured = session is not None
        self.session = session if session is not None else requests.Session()
        # Do not accidentally send test traffic through environment-configured proxies.
        self.session.trust_env = False

    def close(self):
        self.session.close()

    def request(self, method, suffix, original=None, expected=None):
        before = time.perf_counter()
        options = {"catch_response": True, "name": "/links" if method == "POST" else "/links/{code}"} if self.measured else {}
        try:
            with self.session.request(
                method, self.base + suffix,
                json={"url": original} if method == "POST" else None,
                headers={"Accept": "application/json", "Accept-Encoding": "identity"},
                timeout=self.timeout, allow_redirects=False, stream=True, **options,
            ) as response:
                try:
                    if getattr(response, "error", None):
                        raise response.error
                    if response.status_code != 200:
                        raise RequestFailure(f"HTTP_{response.status_code}")
                    data = response.raw.read(MAX_RESPONSE + 1, decode_content=True)
                    if len(data) > MAX_RESPONSE:
                        raise RequestFailure("response_too_large")
                    if response.headers.get("Content-Type", "").split(";", 1)[0].strip().lower() != "application/json":
                        raise RequestFailure("invalid_content_type")
                    try:
                        payload = json.loads(data)
                    except (ValueError, UnicodeError, RecursionError):
                        raise RequestFailure("invalid_JSON") from None
                    if method == "GET":
                        check_resolve(payload, original)
                    else:
                        code = short_code(payload)
                        if expected is not None and code != expected:
                            raise RequestFailure("changed_code")
                    if self.measured:
                        response.success()
                    return payload
                except Exception as error:
                    failure = request_failure(error)
                    if self.measured:
                        response.failure(str(failure))
                    raise failure from None
                finally:
                    if self.measured:
                        # Locust normally stops timing streaming responses at headers.
                        response.request_meta["response_time"] = (time.perf_counter() - before) * 1000
                    if response.raw is not None:
                        response.close()
        except RequestFailure:
            raise
        except Exception as error:
            raise request_failure(error) from None


def request_failure(error):
    if isinstance(error, RequestFailure):
        return error
    # urllib3 can wrap read timeouts in ProtocolError or ConnectionError.
    causes = [error]
    seen = set()
    while causes:
        cause = causes.pop()
        if id(cause) in seen:
            continue
        seen.add(id(cause))
        if "timeout" in type(cause).__name__.lower():
            return RequestFailure("timeout")
        if "ssl" in type(cause).__name__.lower():
            return RequestFailure("TLS_error")
        causes.extend(item for item in getattr(cause, "args", ()) if isinstance(item, Exception))
        if cause.__cause__ is not None:
            causes.append(cause.__cause__)
    return RequestFailure("connection_error")


def check_resolve(payload, original):
    if not isinstance(payload, dict) or payload.get("url") != original:
        raise RequestFailure("invalid_response")


@dataclass
class Result:
    started: str = field(default_factory=lambda: datetime.now(timezone.utc).isoformat())
    elapsed: float = 0
    prepared: int = 0
    reason: str = "not_started"
    samples: dict = field(default_factory=lambda: {"GET": [], "POST": []})
    errors: dict = field(default_factory=lambda: {"GET": Counter(), "POST": Counter()})

    @property
    def total(self):
        return sum(len(values) for values in self.samples.values())

    @property
    def failures(self):
        return sum(sum(values.values()) for values in self.errors.values())

    @property
    def passed(self):
        return self.reason in ("duration", "max_requests") and self.total > 0 and not self.failures


def experiment(config, stop):
    result = Result()
    dataset = []
    client = Client(config)
    try:
        for index in range(config.dataset_size):
            if stop.is_set():
                result.reason = "interrupted"
                return result
            original = f"https://example.com/shortener-loadtest/v1/{index}"
            code = short_code(client.request("POST", "/links", original))
            check_resolve(client.request("GET", "/links/" + code, original), original)
            dataset.append((original, code))
            result.prepared += 1
    except RequestFailure as error:
        result.reason = "preparation_failed: " + str(error)
        return result
    finally:
        client.close()

    gate = gevent.event.Event()
    next_index = 0
    deadline = 0.0
    worker_failed = False

    class LoadUser(HttpUser):
        host = config.base_url

        def on_start(self):
            self.adapter = Client(config, self.client)

        def on_stop(self):
            self.adapter.close()

        @task
        def mixed(self):
            nonlocal next_index
            gate.wait()
            if stop.is_set() or time.perf_counter() >= deadline or next_index >= config.max_requests:
                raise StopUser()
            # Gevent scheduling is cooperative: reserve without yielding.
            index = next_index
            next_index += 1
            method = "POST" if index % 20 == 19 else "GET"
            original, code = dataset[index % len(dataset)]
            before = time.perf_counter()
            failure = None
            try:
                self.adapter.request(method, "/links" if method == "POST" else "/links/" + code,
                                     original, expected=code)
            except RequestFailure as error:
                failure = str(error)
            result.samples[method].append((time.perf_counter() - before, failure is None))
            if failure:
                result.errors[method][failure] += 1

    environment = Environment(user_classes=[LoadUser])

    def user_error(**_):
        nonlocal worker_failed
        worker_failed = True
        stop.set()

    environment.events.user_error.add_listener(user_error)
    runner = environment.create_local_runner()
    started = 0.0
    try:
        runner.start(config.concurrency, spawn_rate=config.concurrency)
        runner.spawning_greenlet.join()
        started = time.perf_counter()
        deadline = started + config.duration
        gate.set()
        while runner.user_count:
            gevent.sleep(0.01)
        result.reason = ("worker_failed" if worker_failed else "interrupted" if stop.is_set() else
                         "max_requests" if next_index >= config.max_requests else "duration")
    finally:
        result.elapsed = time.perf_counter() - started if started else 0
        runner.quit()

    return result


def cell(value):
    return str(value).replace("|", "\\|").replace("\r", " ").replace("\n", " ")


def latency_row(method, outcome, samples):
    values = sorted(duration * 1000 for duration, success in samples if success == outcome)
    if not values:
        return f"| {method} | {'успех' if outcome else 'ошибка'} | 0 | — | — | — | — | — |"
    percentiles = [values[math.ceil(len(values) * q) - 1] for q in (0.5, 0.95, 0.99)]
    metrics = [sum(values) / len(values), *percentiles, values[-1]]
    return (f"| {method} | {'успех' if outcome else 'ошибка'} | {len(values)} | "
            + " | ".join(f"{value:.3f}" for value in metrics) + " |")


def markdown(config, result):
    rate = result.total / result.elapsed if result.elapsed else 0
    successful = (result.total - result.failures) / result.elapsed if result.elapsed else 0
    error_rate = result.failures / result.total * 100 if result.total else 0
    lines = [
        "# Нагрузочный тест URL Shortener", "",
        f"Результат: **{'PASS' if result.passed else 'FAIL'}**. Причина остановки: {result.reason}.", "",
        "| Параметр | Значение |", "|---|---|",
        f"| Начало подготовки, UTC | {result.started} |",
        f"| Стенд | {cell(config.base_url)} |",
        f"| Окружение | {cell(config.label)} |",
        f"| Инструменты | Locust {LOCUST_VERSION}, Python {sys.version.split()[0]} |",
        "| Профиль | Locust: 95% GET / 5% повторных POST, закрытая модель нагрузки |",
        f"| Длительность заданная / фактическая | {config.duration:g} / {result.elapsed:.3f} с |",
        f"| Пользователи Locust / таймаут I/O | {config.concurrency} / {config.timeout:g} с |",
        f"| Набор подготовлен / задан | {result.prepared} / {config.dataset_size} |",
        f"| Лимит измеряемых запросов | {config.max_requests} |",
        f"| Запросы / ошибки | {result.total} / {result.failures} ({error_rate:.2f}%) |",
        f"| Общий / успешный RPS | {rate:.2f} / {successful:.2f} |", "",
        "Задержки в миллисекундах, включая чтение и проверку ответа; перцентили nearest-rank.", "",
        "| Метод | Исход | Количество | Средняя | p50 | p95 | p99 | Максимум |",
        "|---|---|---:|---:|---:|---:|---:|---:|",
    ]
    for method in ("GET", "POST"):
        for outcome in (True, False):
            lines.append(latency_row(method, outcome, result.samples[method]))
    errors = [f"{method}: {category} × {count}"
              for method in ("GET", "POST")
              for category, count in sorted(result.errors[method].items())]
    lines.extend(["", "Ошибки: " + ("; ".join(errors) if errors else "нет в измеряемой фазе") + ".", "",
                  "Подготовка исключена из метрик. Завершение запросов в полёте входит в фактическое время.",
                  "Это результат конкретного стенда, сети и Locust-клиента, а не оценка числа пользователей.",
                  "Порогов RPS и задержки нет; любая ошибка означает FAIL.", ""])
    return "\n".join(lines)


def save_report(config, result, directory=REPORTS):
    directory.mkdir(parents=True, exist_ok=True)
    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S.%fZ")
    identifier = stamp + "-" + uuid.uuid4().hex[:8]
    history = directory / (identifier + ".md")
    content = markdown(config, result)
    with history.open("x", encoding="utf-8") as output:
        output.write(content)
    temporary = directory / (identifier + ".tmp")
    try:
        temporary.write_text(content, encoding="utf-8")
        temporary.replace(directory / "latest.md")
    finally:
        temporary.unlink(missing_ok=True)
    return history


def main(argv=None):
    config = arguments(argv)
    stop = threading.Event()
    previous = signal.signal(signal.SIGINT, lambda *_: stop.set())
    try:
        result = experiment(config, stop)
        path = save_report(config, result)
    except OSError:
        print("Не удалось сохранить отчёт или выделить системные ресурсы.")
        return 1
    finally:
        signal.signal(signal.SIGINT, previous)
    print(f"{'PASS' if result.passed else 'FAIL'}: {result.total} запросов, "
          f"{result.failures} ошибок. Отчёт: {path}")
    return 130 if result.reason == "interrupted" else 0 if result.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
