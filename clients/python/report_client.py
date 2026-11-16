#!/usr/bin/env python3
"""A sample client for the Frontforge Report Server HTTP API.

It authenticates with an API key, lists the templates, renders one PDF
straight away (POST /v1/render), renders a batch in the background
(POST /v1/jobs), waits for that job and downloads what it made, and shows
what an error looks like. `python report_client.py events` listens to
GET /v1/events instead.

Standard library only, Python 3.9 or later. It is configured by environment
variables: see README.md.
"""

from __future__ import annotations

import base64
import http.client
import json
import os
import sys
import time
import urllib.error
import urllib.request
import uuid
from pathlib import Path
from typing import Any, Iterator


class ApiError(Exception):
    """An answer that wasn't 2xx.

    The server answers errors as JSON: {"error": <kind>, "message": ...}, and
    a render the engine refused adds "diagnostics", each with code, severity,
    message and where in the template it is.
    """

    def __init__(
        self,
        status: int,
        kind: str,
        message: str,
        diagnostics: list[dict[str, Any]],
        request_id: str | None,
        retry_after: int | None,
    ) -> None:
        super().__init__(f"{status} {kind}: {message}")
        self.status = status
        self.kind = kind
        self.message = message
        self.diagnostics = diagnostics
        self.request_id = request_id
        self.retry_after = retry_after

    @classmethod
    def from_response(cls, status: int, headers: Any, body: bytes) -> "ApiError":
        kind, message, diagnostics = "", "", []
        try:
            doc = json.loads(body)
            if isinstance(doc, dict):
                kind = str(doc.get("error") or "")
                message = str(doc.get("message") or "")
                diagnostics = doc.get("diagnostics") or []
        except ValueError:
            message = body.decode("utf-8", "replace").strip()
        retry = headers.get("Retry-After")
        return cls(
            status,
            kind or f"http-{status}",
            message or http.client.responses.get(status, kind),
            diagnostics,
            headers.get("X-Request-Id"),
            int(retry) if retry and retry.isdigit() else None,
        )


class Response:
    def __init__(self, status: int, headers: Any, body: bytes) -> None:
        self.status = status
        self.headers = headers
        self.body = body

    def json(self) -> Any:
        return json.loads(self.body)

    def diagnostics(self) -> list[dict[str, Any]]:
        """A render's warnings: X-Rfml-Diagnostics, JSON in unpadded base64url."""
        value = self.headers.get("X-Rfml-Diagnostics")
        if not value:
            return []
        return json.loads(base64.urlsafe_b64decode(value + "=" * (-len(value) % 4)))


class ReportServer:
    """The few calls an application makes, over urllib."""

    def __init__(self, base_url: str, api_key: str, timeout: float = 120) -> None:
        self.base_url = base_url.rstrip("/")
        self.api_key = api_key
        self.timeout = timeout

    def _open(self, method: str, path: str, body: Any = None, headers: dict[str, str] | None = None,
              timeout: float | None = None) -> Any:
        req = urllib.request.Request(self.base_url + path, method=method)
        # An API key goes in the Authorization header as a bearer token.
        req.add_header("Authorization", f"Bearer {self.api_key}")
        # Our own request id, which the server logs and sends back: quote it
        # to whoever runs the server when something goes wrong.
        req.add_header("X-Request-Id", uuid.uuid4().hex[:16])
        for name, value in (headers or {}).items():
            req.add_header(name, value)
        if body is not None:
            req.add_header("Content-Type", "application/json")
            req.data = json.dumps(body).encode("utf-8")
        return urllib.request.urlopen(req, timeout=timeout or self.timeout)

    def request(self, method: str, path: str, body: Any = None,
                headers: dict[str, str] | None = None) -> Response:
        """Sends a request, waits out a rate limit (429) up to three times,
        and raises ApiError for any other answer that isn't 2xx."""
        for attempt in range(4):
            try:
                with self._open(method, path, body, headers) as res:
                    return Response(res.status, res.headers, res.read())
            except urllib.error.HTTPError as e:
                err = ApiError.from_response(e.code, e.headers, e.read())
                if e.code == 429 and attempt < 3:
                    wait = min(err.retry_after or 1, 30)
                    print(f"  rate limited; trying again in {wait} s")
                    time.sleep(wait)
                    continue
                raise err from None
        raise AssertionError("unreachable")

    def me(self) -> dict[str, Any]:
        return self.request("GET", "/v1/me").json()

    def templates(self) -> list[dict[str, Any]]:
        return self.request("GET", "/v1/templates").json()["templates"]

    def render(self, template_id: str, environment: str, datasets: dict[str, Any],
               fmt: str = "pdf", options: dict[str, Any] | None = None) -> Response:
        body: dict[str, Any] = {"templateId": template_id, "format": fmt, "datasets": datasets}
        if environment:
            body["environment"] = environment
        if options:
            body["options"] = options
        return self.request("POST", "/v1/render", body)

    def submit_job(self, template_id: str, environment: str, runs: list[dict[str, Any]],
                   idempotency_key: str, fmt: str = "pdf") -> dict[str, Any]:
        body: dict[str, Any] = {"templateId": template_id, "format": fmt, "runs": runs}
        if environment:
            body["environment"] = environment
        # The same key with the same request gets the same job back, so a
        # submission that timed out can be sent again safely.
        return self.request("POST", "/v1/jobs", body, {"Idempotency-Key": idempotency_key}).json()

    def job(self, job_id: str) -> dict[str, Any]:
        return self.request("GET", f"/v1/jobs/{job_id}").json()

    def download(self, report_id: str) -> bytes:
        return self.request("GET", f"/v1/reports/{report_id}").body

    def events(self) -> Iterator[tuple[str, Any]]:
        """GET /v1/events as (event, data) pairs, until the stream ends.

        A server-sent event is "event:" and "data:" lines ended by a blank
        line; lines starting with ":" are keep-alive comments (every 25 s).
        """
        try:
            # Longer than the 25 s between keep-alives.
            res = self._open("GET", "/v1/events", headers={"Accept": "text/event-stream"}, timeout=60)
        except urllib.error.HTTPError as e:
            raise ApiError.from_response(e.code, e.headers, e.read()) from None
        with res:
            name, data = "message", []
            for raw in res:
                line = raw.decode("utf-8").rstrip("\r\n")
                if not line:
                    if data:
                        yield name, json.loads("\n".join(data))
                    name, data = "message", []
                elif line.startswith(":"):
                    continue
                else:
                    field, _, value = line.partition(":")
                    value = value[1:] if value.startswith(" ") else value
                    if field == "event":
                        name = value
                    elif field == "data":
                        data.append(value)


def wait_for_job(server: ReportServer, job_id: str, timeout: float = 300) -> dict[str, Any]:
    """Polls the job once a second until it is done, error or cancelled.

    A job can instead call your application back when it finishes: see
    callbackUrl in the API reference.
    """
    deadline = time.monotonic() + timeout
    seen = None
    while True:
        job = server.job(job_id)
        progress = job.get("progress") or {}
        state = (job["status"], progress.get("done"))
        if state != seen:
            print(f"  {job['status']}: {progress.get('done', 0)} of {progress.get('total', '?')} rendered")
            seen = state
        if job["status"] in ("done", "error", "cancelled"):
            return job
        if time.monotonic() > deadline:
            raise TimeoutError(f"job {job_id} is still {job['status']} after {timeout:.0f} s")
        time.sleep(1)


def print_error(e: ApiError) -> None:
    print(f"  {e.status} {e.kind}: {e.message}")
    if e.request_id:
        print(f"  request id {e.request_id}")
    errors = [d for d in e.diagnostics if d.get("severity") == "error"]
    for d in errors[:5]:
        where = d.get("node") or d.get("path") or ""
        print(f"  - {d.get('code')} at {where}: {d.get('message')}")
    rest = len(e.diagnostics) - min(len(errors), 5)
    if rest > 0:
        print(f"  ... and {rest} more diagnostics (warnings included)")


def env(name: str, default: str = "") -> str:
    return os.environ.get(name, "").strip() or default


# The gallery example letters.json is for. A report copied from it keeps
# this name, but not always one id: New report in the studio makes the id
# from the title (letter-to-customers), while an empty catalog's "Start
# from an example" keeps the example's own (customer-letter), and either
# ends up with another when that one is taken. GET /v1/templates doesn't say
# which example a template came from, so without REPORT_TEMPLATE the
# samples look for this name.
EXAMPLE_NAME = "Letter to customers"


def pick_template(templates: list[dict[str, Any]], wanted: str, environment: str) -> tuple[str, str]:
    """The template to render: REPORT_TEMPLATE's, else the one published as
    EXAMPLE_NAME. Returns its id and a line to print, or "" and why not."""
    where = f" in {environment}" if environment else ""

    def published(t: dict[str, Any]) -> bool:
        return environment in t["published"] if environment else bool(t["published"])

    if wanted:
        t = next((t for t in templates if t["id"] == wanted), None)
        if t is None:
            return "", f"There's no template {wanted}. Set REPORT_TEMPLATE to one of the ids above."
        if not published(t):
            return "", f"{wanted} isn't published{where}. Publish it in the studio."
        return wanted, f"Using {wanted}, from REPORT_TEMPLATE."
    named = [t for t in templates if t["name"].strip().casefold() == EXAMPLE_NAME.casefold()]
    if not named:
        return "", (f"No template is called {EXAMPLE_NAME}. In the studio, choose New report, then From a template, "
                    f"pick {EXAMPLE_NAME} and publish it; or set REPORT_TEMPLATE to another template's id.")
    live = [t for t in named if published(t)]
    if not live:
        ids = ", ".join(t["id"] for t in named)
        return "", f"No template called {EXAMPLE_NAME} is published{where} ({ids}). Publish it in the studio, or set REPORT_TEMPLATE."
    if len(live) > 1:
        return live[0]["id"], (f"Using {live[0]['id']}, the first of {len(live)} published templates called "
                               f"{EXAMPLE_NAME}. Set REPORT_TEMPLATE to choose another.")
    return live[0]["id"], f"Using {live[0]['id']}, the published template called {EXAMPLE_NAME}."


def run(server: ReportServer) -> int:
    environment = env("REPORT_ENVIRONMENT")
    out = Path(env("REPORT_OUT", "out"))
    # One element per run: each is that run's datasets, by dataset name.
    runs: list[dict[str, Any]] = json.loads(Path(env("REPORT_DATA", "letters.json")).read_text("utf-8"))

    me = server.me()
    print(f"Authenticated as {me['kind']} {me['name']}: {', '.join(me['permissions'])}")

    print("\nTemplates:")
    templates = server.templates()
    for t in templates:
        where = ", ".join(t["published"]) or "not published"
        print(f"  {t['id']:<24} {t['name']} ({where})")
    template_id, line = pick_template(templates, env("REPORT_TEMPLATE"), environment)
    print(f"\n{line}")
    if not template_id:
        return 1

    out.mkdir(parents=True, exist_ok=True)

    print(f"\n1. Rendering {template_id} now (POST /v1/render)")
    res = server.render(template_id, environment, runs[0])
    path = out / f"{template_id}.pdf"
    path.write_bytes(res.body)
    print(f"  wrote {path}: {len(res.body)} bytes, {res.headers.get('X-Rfml-Pages')} page(s), "
          f"report {res.headers.get('X-Report-Id')}, template commit {res.headers.get('X-Template-Commit', '')[:8]}")
    # A render that succeeds can still have warnings, such as data that
    # doesn't match the template's schema.
    for d in res.diagnostics():
        print(f"  {d.get('severity')} {d.get('code')}: {d.get('message')}")

    print(f"\n2. Rendering {len(runs)} in the background (POST /v1/jobs)")
    job = server.submit_job(template_id, environment, [{"datasets": d} for d in runs], str(uuid.uuid4()))
    print(f"  job {job['id']} is {job['status']}")
    job = wait_for_job(server, job["id"])
    if job["status"] != "done":
        print(f"  the job ended {job['status']}: {job.get('error', '')}")
        return 1
    for n, report_id in enumerate(job["reportIds"], 1):
        try:
            pdf = server.download(report_id)
        except ApiError as e:
            if e.status == 403:
                print("  the key may render but not download stored reports: give it report.read (see README.md)")
                return 1
            raise
        path = out / f"{template_id}-{n}.pdf"
        path.write_bytes(pdf)
        print(f"  wrote {path}: {len(pdf)} bytes")

    # By default, data that breaks the template's schema is only a warning,
    # and what can't be bound renders empty; strict makes it an error.
    print("\n3. Rendering empty data in strict schema mode, to show an error")
    try:
        server.render(template_id, environment, {name: {} for name in runs[0]},
                      options={"schemaMode": "strict"})
        print("  it rendered anyway")
    except ApiError as e:
        print_error(e)
    return 0


def listen(server: ReportServer) -> int:
    """Prints the server's events until the sign-in ends or Ctrl+C.

    When the server restarts the stream ends after a `restarting` event:
    connect again, as a browser's EventSource does by itself.
    """
    print("Listening to GET /v1/events; Ctrl+C stops.")
    while True:
        try:
            for name, data in server.events():
                print(f"  {name}: {json.dumps(data)}")
                if name == "signed-out":
                    return 0
        except ApiError as e:
            print_error(e)
            return 1
        except OSError as e:
            print(f"  the stream broke ({e}); connecting again")
        time.sleep(1)


def main(argv: list[str]) -> int:
    api_key = env("REPORT_SERVER_API_KEY")
    if not api_key:
        print("Set REPORT_SERVER_API_KEY to an API key (Admin > API keys).", file=sys.stderr)
        return 2
    server = ReportServer(env("REPORT_SERVER_URL", "http://localhost:43123"), api_key)
    try:
        if argv[1:] == ["events"]:
            return listen(server)
        if argv[1:]:
            print(f"usage: {argv[0]} [events]", file=sys.stderr)
            return 2
        return run(server)
    except ApiError as e:
        print_error(e)
        return 1
    except KeyboardInterrupt:
        return 130
    except urllib.error.URLError as e:
        print(f"Can't reach {server.base_url}: {e.reason}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
