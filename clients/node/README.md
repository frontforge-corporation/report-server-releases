# TypeScript sample client

A sample client for Frontforge Report Server in TypeScript, on Node.js with its built-in `fetch`. [The samples' README](../README.md) says what it does, how to make the API key and publish the template it renders, and what each environment variable is.

Node runs the `.ts` files as they are, by stripping the types, so there is no build step and nothing to install to run it. TypeScript is only a development dependency, to check the types.

## Prerequisites

- Node.js 22.18 or later.
- A Frontforge Report Server, an API key and the published template, as [the samples' README](../README.md#before-you-run-one) says.

## Run it

From this folder:

```bash
export REPORT_SERVER_URL=http://localhost:43123
export REPORT_SERVER_API_KEY=rk_...
node main.ts
```

On Windows, in PowerShell, set them with `$env:REPORT_SERVER_API_KEY = "rk_..."`. The PDFs go in `out/`.

To listen to the server's events instead, until Ctrl+C:

```bash
node main.ts events
```

To check the types:

```bash
npm install
npm run check
```

## The code

- `client.ts` is the client to copy: `ReportServer` with `me`, `templates`, `render`, `submitJob`, `job`, `waitForJob`, `download` and `events` (an async generator). `request` sends each call with the API key and a request id, waits out a `429`, and throws an `ApiError` for any other answer that isn't 2xx: `status`, `kind` (the body's `error`), `message`, `diagnostics`, `requestId` and `retryAfter`.
- `main.ts` is the walk-through (`run`) and the event listener (`listen`), which connects again after the server restarts.

In a browser, use `EventSource` for the events instead: it can't send an `Authorization` header, so it suits a page signed in to the studio, not an API key. Keep API keys on your servers.
