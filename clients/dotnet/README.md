# .NET sample client

A sample client for Frontforge Report Server in C#, with `HttpClient` and `System.Text.Json` only: no NuGet packages. [The samples' README](../README.md) says what it does, how to make the API key and publish the template it renders, and what each environment variable is.

## Prerequisites

- The .NET 10 SDK.
- A Frontforge Report Server, an API key and the published template, as [the samples' README](../README.md#before-you-run-one) says.

## Run it

From this folder:

```bash
export REPORT_SERVER_URL=http://localhost:43123
export REPORT_SERVER_API_KEY=rk_...
dotnet run
```

On Windows, in PowerShell, set them with `$env:REPORT_SERVER_API_KEY = "rk_..."`. The PDFs go in `out/`.

To listen to the server's events instead, until Ctrl+C:

```bash
dotnet run -- events
```

## The code

- `ReportServerClient.cs` is the client to copy: `ReportServerClient` with `MeAsync`, `TemplatesAsync`, `RenderAsync`, `SubmitJobAsync`, `JobAsync`, `WaitForJobAsync`, `DownloadAsync` and `EventsAsync` (an `IAsyncEnumerable`). Each call sends the API key and a request id, waits out a `429`, and throws an `ApiException` for any other answer that isn't 2xx: `Status`, `Kind` (the body's `error`), `Message`, `Diagnostics`, `RequestId` and `RetryAfter`. Datasets are `JsonObject`s, so any data your application has can go in.
- `Program.cs` is the walk-through (`RunAsync`) and the event listener (`ListenAsync`), which connects again after the server restarts. The event stream stays open, so it uses an `HttpClient` without a timeout for it.

In an ASP.NET Core application, register the client with `AddHttpClient` and give it the base address and key from configuration rather than environment variables.
