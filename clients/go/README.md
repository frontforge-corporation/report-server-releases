# Go sample client

A sample client for Frontforge Report Server in Go, with the standard library only (`net/http`, `encoding/json`). [The samples' README](../README.md) says what it does, how to make the API key and publish the template it renders, and what each environment variable is.

## Prerequisites

- Go 1.22 or later. No modules to download.
- A Frontforge Report Server, an API key and the published template, as [the samples' README](../README.md#before-you-run-one) says.

## Run it

From this folder:

```bash
export REPORT_SERVER_URL=http://localhost:43123
export REPORT_SERVER_API_KEY=rk_...
go run .
```

On Windows, in PowerShell, set them with `$env:REPORT_SERVER_API_KEY = "rk_..."`. The PDFs go in `out/`.

To listen to the server's events instead, until Ctrl+C:

```bash
go run . events
```

## The code

- `client.go` is the client to copy: `Client` with `Me`, `Templates`, `Render`, `SubmitJob`, `Job`, `WaitForJob`, `Download` and `Events`. Each call sends the API key and a request id, waits out a `429`, and returns an `*APIError` for any other answer that isn't 2xx: `Status`, `Kind` (the body's `error`), `Message`, `Diagnostics`, `RequestID` and `RetryAfter`. Check for it with `errors.As`.
- `main.go` is the walk-through (`run`) and the event listener (`listen`), which connects again after the server restarts. The event stream stays open, so `listen` uses an `http.Client` without a timeout and stops on its context instead.
