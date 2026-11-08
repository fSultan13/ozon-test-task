#!/usr/bin/env python3
"""Check both demo origins without external Python packages."""

import argparse
import json
import time
import urllib.error
import urllib.request
from pathlib import Path


def request(base, path, payload=None):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(base + path, data=data)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        response = urllib.request.urlopen(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        return response.status, response.read(), response.headers


def check(base, label):
    for path in ("/healthz", "/readyz"):
        status, body, _ = request(base, path)
        assert status == 200 and json.loads(body) == {"status": "ok"}, path
    for asset in ("", "swagger-ui.css", "swagger-ui-bundle.js", "swagger-ui-standalone-preset.js"):
        status, body, _ = request(base, "/swagger/" + asset)
        assert status == 200 and body, asset
    status, body, _ = request(base, "/swagger/swagger-initializer.js")
    assert status == 200 and b'url: "/swagger/swagger.json"' in body
    assert b"validatorUrl: null" in body
    status, body, _ = request(base, "/swagger/swagger.json")
    assert status == 200
    spec = json.loads(body)
    assert not spec.get("host") and not spec.get("schemes")
    assert "/links" in spec["paths"] and "/links/{code}" in spec["paths"]

    original = f"https://example.com/demo-check/{time.time_ns()}/{label}"
    status, body, headers = request(base, "/links", {"url": original})
    assert status == 200 and headers.get("X-Request-ID")
    short = json.loads(body)["short_url"]
    assert short.startswith(base + "/links/")
    code = short.rsplit("/", 1)[1]
    assert len(code) == 10 and all(c in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_" for c in code)
    status, body, _ = request(base, "/links", {"url": original})
    assert status == 200 and json.loads(body) == {"short_url": short}
    status, body, _ = request(base, "/links/" + code)
    assert status == 200 and json.loads(body) == {"url": original}
    status, body, _ = request(base, "/links", {"url": "invalid"})
    assert status == 400 and json.loads(body) == {"code": "invalid_request", "message": "Invalid request"}
    status, body, _ = request(base, "/links", {"url": original, "extra": True})
    assert status == 400
    print(f"PASS: {label}: health, Swagger assets/spec, create, repeat, resolve, validation")
    return {"code": code, "url": original}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--memory", default="http://127.0.0.1:18080")
    parser.add_argument("--postgres", default="http://127.0.0.1:18081")
    parser.add_argument("--save", type=Path)
    parser.add_argument("--after-restart", type=Path)
    args = parser.parse_args()
    if args.after_restart:
        records = json.loads(args.after_restart.read_text())
        status, _, _ = request(args.memory, "/links/" + records["memory"]["code"])
        assert status == 404, "memory survived process restart"
        record = records["postgres"]
        status, body, _ = request(args.postgres, "/links/" + record["code"])
        assert status == 200 and json.loads(body) == {"url": record["url"]}
        print("PASS: memory cleared and PostgreSQL preserved after container recreation")
        return
    records = {"memory": check(args.memory, "memory"), "postgres": check(args.postgres, "postgres")}
    if args.save:
        args.save.write_text(json.dumps(records))


if __name__ == "__main__":
    main()
