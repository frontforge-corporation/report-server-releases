# Sample clients

Small programs that show how an application talks to Frontforge Report Server through its HTTP API, in four languages:

| Folder | Language | Needs |
| --- | --- | --- |
| [`dotnet/`](dotnet/) | C# | .NET 10 SDK |
| [`python/`](python/) | Python | Python 3.9 or later |
| [`go/`](go/) | Go | Go 1.22 or later |
| [`node/`](node/) | TypeScript | Node.js 22.18 or later |

Each uses only its language's own HTTP client and JSON support: no SDK, and no packages at run time. Copy the one you need into your application and change it as you like: they are under the MIT licence ([`LICENSE`](LICENSE)).

## What they do

Each sample does the same five things, in the same order:

1. **Authenticates with an API key.** `GET /v1/me` says who the key is and what it may do.
2. **Lists the templates** (`GET /v1/templates`), with the environments each is published in.
3. **Renders a PDF straight away** (`POST /v1/render`) from a published template and the application's own data, and writes it to a file.
4. **Renders a batch in the background** (`POST /v1/jobs`): one PDF per run, each with its own data. It polls the job (`GET /v1/jobs/{id}`) until it has finished, then downloads each PDF (`GET /v1/reports/{id}`).
5. **Shows an error:** it renders empty data in strict schema mode, and prints the error the server answers and the diagnostics that say what's wrong.

Given `events`, each listens to `GET /v1/events` instead, and prints the server's events as they come.

The data is in `letters.json`, one element per run: three letters for **Letter to customers**, one of the examples in the studio's gallery.

## Before you run one

1. **A server.** Any Frontforge Report Server, such as the Docker install in the [releases repository's README](../README.md). The samples assume `http://localhost:43123`.
2. **The template.** In the studio's catalog, choose **New report**, then **From a template** (on an empty catalog, **Start from an example**), pick **Letter to customers**, and publish it. Keep its title: the samples find it by that name, because its id depends on how it was made. **New report** makes the id from the title, `letter-to-customers`, and **Start from an example** keeps the example's own, `customer-letter`; either can be another when that one is taken. With several published under that name, the samples use the first and say so; set `REPORT_TEMPLATE` to the id of the one you want. Another template works too: set `REPORT_TEMPLATE` and give `REPORT_DATA` a file of its data.
3. **An API key.** Applications sign in with an API key, not a user's password. In the studio, open **Admin → API keys → New key**, give it a name and the role **service**, and copy its secret: it's shown once. Send it as `Authorization: Bearer <key>` on every request.

   The `service` role may read templates, render and use live data, but not download stored reports, which step 4 needs. For that, add a custom role with `report.read`: under **Admin → Roles**, create one (say `report-download`) with only `report.read`, and give the key both roles. A key that renders only straight away (step 3) needs `service` alone.

## Running one

Every sample reads the same environment variables:

| Variable | What | Default |
| --- | --- | --- |
| `REPORT_SERVER_URL` | The server's address | `http://localhost:43123` |
| `REPORT_SERVER_API_KEY` | The API key's secret | none: required |
| `REPORT_TEMPLATE` | The template's id | the template published as **Letter to customers** |
| `REPORT_ENVIRONMENT` | The environment to render from, such as `prod` | the server's first environment |
| `REPORT_DATA` | A JSON array with each run's datasets, by dataset name | `letters.json` |
| `REPORT_OUT` | The folder the PDFs go in | `out` |

An install with several environments (`dev`, `test`, `uat`, `prod`) renders from the first, `dev`, when a request names none, so a production application should always set `REPORT_ENVIRONMENT`.

From the sample's folder, with the variables set, run it as its README says. A run looks like this:

```text
Authenticated as key billing-app: data.live, report.read, report.render, template.read

Templates:
  letter-to-customers      Letter to customers (dev)

Using letter-to-customers, the published template called Letter to customers.

1. Rendering letter-to-customers now (POST /v1/render)
  wrote out/letter-to-customers.pdf: 27627 bytes, 1 page(s), report e2bca71f72320c91622e681e, template commit c7d6cf7c

2. Rendering 3 in the background (POST /v1/jobs)
  job 0b8e46d96cb2bec3a3fe87e736f947e9 is queued
  queued: 0 of 3 rendered
  done: 3 of 3 rendered
  wrote out/letter-to-customers-1.pdf: 27627 bytes
  wrote out/letter-to-customers-2.pdf: 27512 bytes
  wrote out/letter-to-customers-3.pdf: 27567 bytes

3. Rendering empty data in strict schema mode, to show an error
  422 invalid: The document has errors and errorPolicy is fail; nothing was rendered.
  request id 5a575274f2e8409d
  - bind/schema-violation at /main: data.main: required property `letter` is missing
  ...
```

## The API in brief

The [API reference](https://docs.frontforge.com/reference/api/) has every route; these are the parts a client meets first.

- **Straight away or in the background.** `POST /v1/render` answers with the document's bytes, so it suits one document a user waits for. `POST /v1/jobs` queues many renders, one per run, and suits batches: the job survives a restart of the server and retries what may pass later. Instead of polling, a job can call your application back when it finishes (`callbackUrl`, signed as Standard Webhooks); the reference says how.
- **Safe retries.** Send an `Idempotency-Key` with `POST /v1/jobs`. Sent again with the same key and request, say after a timeout, it returns the job it first made instead of queueing another.
- **What came back.** A render's response headers say which published version was used (`X-Template-Commit`), how many pages it has (`X-Rfml-Pages`), and, for a PDF, the id it's stored under (`X-Report-Id`), to download it again later. A render that succeeds can still have warnings, such as data that doesn't match the template's schema: `X-Rfml-Diagnostics` holds them, as JSON in unpadded base64url, and the samples print them.
- **Errors** are JSON: `{"error": "<kind>", "message": "..."}`. A render the engine refused is `422` with `"error": "invalid"` and `diagnostics`, each with a `code`, a `severity` (error, warning or info), a `message` and where in the template or data it is. Some errors carry only `error`, so fall back to the HTTP status when there's no `message`.
- **Data that doesn't fit.** By default data that breaks the template's schema is a warning, and what can't be bound renders empty. Pass `"options": {"schemaMode": "strict"}` to have it refused instead.
- **Rate limits.** Each API key has its own budget per class of request (`read`, `write`, `render`). Past it the answer is `429` with `Retry-After` in seconds: the samples wait that long and try again, up to three times.
- **Request ids.** Every answer carries `X-Request-Id`. The samples send their own, and print the server's with an error: the server logs it, so it finds the request in the logs.
- **Events.** `GET /v1/events` is a stream of server-sent events: `hello` when connected, `restarting` before the server restarts, `signed-out` when the key is revoked or expires (checked every 25 s), and `library-released`. A comment line every 25 s keeps it open. After `restarting` the stream ends; connect again, and the next `hello` has a new `run`.
