# Python sample client

A sample client for Frontforge Report Server in Python, with the standard library only (`urllib`, `json`). [The samples' README](../README.md) says what it does, how to make the API key and publish the template it renders, and what each environment variable is.

## Prerequisites

- Python 3.9 or later. No packages.
- A Frontforge Report Server, an API key and the published template, as [the samples' README](../README.md#before-you-run-one) says.

## Run it

From this folder:

```bash
export REPORT_SERVER_URL=http://localhost:43123
export REPORT_SERVER_API_KEY=rk_...
python report_client.py
```

On Windows, in PowerShell, set them with `$env:REPORT_SERVER_API_KEY = "rk_..."`. The PDFs go in `out/`.

To listen to the server's events instead, until Ctrl+C:

```bash
python report_client.py events
```

## The code

Everything is in `report_client.py`:

- `ReportServer` makes the calls: `me`, `templates`, `render`, `submit_job`, `job`, `download` and `events`. `request` sends each one with the API key and a request id, waits out a `429`, and raises `ApiError` for any other answer that isn't 2xx.
- `ApiError` holds the server's error: `status`, `kind` (the body's `error`), `message`, `diagnostics`, `request_id` and `retry_after`.
- `wait_for_job` polls a job until it has finished.
- `run` is the walk-through; `listen` prints the events and connects again after the server restarts.
