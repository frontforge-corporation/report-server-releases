// A sample client for the Frontforge Report Server HTTP API.
//
// It authenticates with an API key, lists the templates, renders one PDF
// straight away (POST /v1/render), renders a batch in the background
// (POST /v1/jobs), waits for that job and downloads what it made, and shows
// what an error looks like. `dotnet run -- events` listens to GET /v1/events
// instead.
//
// It is configured by environment variables: see README.md.

using System.Net;
using System.Text.Json.Nodes;
using ReportClient;

static string Env(string name, string fallback = "") =>
    Environment.GetEnvironmentVariable(name)?.Trim() is { Length: > 0 } v ? v : fallback;

static void PrintError(ApiException e)
{
    Console.WriteLine($"  {(int)e.Status} {e.Kind}: {e.Message}");
    if (e.RequestId is not null) Console.WriteLine($"  request id {e.RequestId}");
    var errors = e.Diagnostics.Where(d => d.Severity == "error").Take(5).ToList();
    foreach (var d in errors) Console.WriteLine($"  - {d.Code} at {d.Node ?? d.Path}: {d.Message}");
    var rest = e.Diagnostics.Count - errors.Count;
    if (rest > 0) Console.WriteLine($"  ... and {rest} more diagnostics (warnings included)");
}

// The template to render, REPORT_TEMPLATE's or else the one published as
// ExampleName, and a line to print; or "" and why not.
//
// ExampleName is the gallery example letters.json is for. A report copied
// from it keeps this name, but not always one id: New report in the studio
// makes the id from the title (letter-to-customers), while an empty
// catalog's "Start from an example" keeps the example's own
// (customer-letter), and either ends up with another when that one is
// taken. GET /v1/templates doesn't say which example a template came from,
// so without REPORT_TEMPLATE the samples look for this name.
static (string Id, string Line) PickTemplate(List<Template> templates, string wanted, string? environment)
{
    const string ExampleName = "Letter to customers";
    var where = environment is null ? "" : $" in {environment}";
    bool IsPublished(Template t) => environment is null ? t.Published.Count > 0 : t.Published.ContainsKey(environment);

    if (wanted != "")
    {
        var t = templates.FirstOrDefault(t => t.Id == wanted);
        if (t is null) return ("", $"There's no template {wanted}. Set REPORT_TEMPLATE to one of the ids above.");
        if (!IsPublished(t)) return ("", $"{wanted} isn't published{where}. Publish it in the studio.");
        return (wanted, $"Using {wanted}, from REPORT_TEMPLATE.");
    }
    var named = templates.Where(t => string.Equals(t.Name.Trim(), ExampleName, StringComparison.OrdinalIgnoreCase)).ToList();
    if (named.Count == 0)
        return ("", $"No template is called {ExampleName}. In the studio, choose New report, then From a template, pick {ExampleName} and publish it; or set REPORT_TEMPLATE to another template's id.");
    var live = named.Where(IsPublished).ToList();
    if (live.Count == 0)
        return ("", $"No template called {ExampleName} is published{where} ({string.Join(", ", named.Select(t => t.Id))}). Publish it in the studio, or set REPORT_TEMPLATE.");
    if (live.Count > 1)
        return (live[0].Id, $"Using {live[0].Id}, the first of {live.Count} published templates called {ExampleName}. Set REPORT_TEMPLATE to choose another.");
    return (live[0].Id, $"Using {live[0].Id}, the published template called {ExampleName}.");
}

static async Task<int> RunAsync(ReportServerClient server, CancellationToken ct)
{
    var environment = Env("REPORT_ENVIRONMENT") is { Length: > 0 } e ? e : null;
    var outDir = Env("REPORT_OUT", "out");
    // One element per run: each is that run's datasets, by dataset name.
    var runs = JsonNode.Parse(await File.ReadAllTextAsync(Env("REPORT_DATA", "letters.json"), ct))!
        .AsArray().Select(n => n!.AsObject()).ToList();

    var me = await server.MeAsync(ct);
    Console.WriteLine($"Authenticated as {me.Kind} {me.Name}: {string.Join(", ", me.Permissions)}");

    Console.WriteLine("\nTemplates:");
    var templates = await server.TemplatesAsync(ct);
    foreach (var t in templates)
    {
        var where = t.Published.Count > 0 ? string.Join(", ", t.Published.Keys.Order()) : "not published";
        Console.WriteLine($"  {t.Id,-24} {t.Name} ({where})");
    }
    var (templateId, line) = PickTemplate(templates, Env("REPORT_TEMPLATE"), environment);
    Console.WriteLine($"\n{line}");
    if (templateId == "") return 1;
    Directory.CreateDirectory(outDir);

    Console.WriteLine($"\n1. Rendering {templateId} now (POST /v1/render)");
    var r = await server.RenderAsync(new RenderRequest(templateId, environment, Datasets: runs[0]), ct);
    var path = Path.Combine(outDir, $"{templateId}.pdf");
    await File.WriteAllBytesAsync(path, r.Body, ct);
    Console.WriteLine($"  wrote {path}: {r.Body.Length} bytes, {r.Pages} page(s), report {r.ReportId}, template commit {r.Commit?[..Math.Min(8, r.Commit.Length)]}");
    // A render that succeeds can still have warnings, such as data that
    // doesn't match the template's schema.
    foreach (var d in r.Diagnostics) Console.WriteLine($"  {d.Severity} {d.Code}: {d.Message}");

    Console.WriteLine($"\n2. Rendering {runs.Count} in the background (POST /v1/jobs)");
    var job = await server.SubmitJobAsync(templateId, environment, runs.Select(d => new Run(d)), Guid.NewGuid().ToString(), ct);
    Console.WriteLine($"  job {job.Id} is {job.Status}");
    var seen = "";
    job = await server.WaitForJobAsync(job.Id, j =>
    {
        var s = $"{j.Status}: {j.Progress.Done} of {j.Progress.Total} rendered";
        if (s != seen) Console.WriteLine($"  {seen = s}");
    }, ct);
    if (job.Status != "done")
    {
        Console.WriteLine($"  the job ended {job.Status}: {job.Error}");
        return 1;
    }
    foreach (var (id, n) in (job.ReportIds ?? []).Select((id, i) => (id, i + 1)))
    {
        byte[] pdf;
        try
        {
            pdf = await server.DownloadAsync(id, ct);
        }
        catch (ApiException ex) when (ex.Status == HttpStatusCode.Forbidden)
        {
            Console.WriteLine("  the key may render but not download stored reports: give it report.read (see README.md)");
            return 1;
        }
        path = Path.Combine(outDir, $"{templateId}-{n}.pdf");
        await File.WriteAllBytesAsync(path, pdf, ct);
        Console.WriteLine($"  wrote {path}: {pdf.Length} bytes");
    }

    // By default, data that breaks the template's schema is only a warning,
    // and what can't be bound renders empty; strict makes it an error.
    Console.WriteLine("\n3. Rendering empty data in strict schema mode, to show an error");
    var empty = new JsonObject();
    foreach (var (name, _) in runs[0]) empty[name] = new JsonObject();
    try
    {
        await server.RenderAsync(new RenderRequest(templateId, environment, Datasets: empty,
            Options: new JsonObject { ["schemaMode"] = "strict" }), ct);
        Console.WriteLine("  it rendered anyway");
    }
    catch (ApiException ex)
    {
        PrintError(ex);
    }
    return 0;
}

// Prints the server's events until the sign-in ends or Ctrl+C. When the
// server restarts, the stream ends after a `restarting` event: connect
// again, as a browser's EventSource does by itself.
static async Task<int> ListenAsync(ReportServerClient server, CancellationToken ct)
{
    Console.WriteLine("Listening to GET /v1/events; Ctrl+C stops.");
    while (true)
    {
        try
        {
            await foreach (var e in server.EventsAsync(ct))
            {
                Console.WriteLine($"  {e.Name}: {e.Data?.ToJsonString()}");
                if (e.Name == "signed-out") return 0;
            }
        }
        catch (Exception ex) when (ex is HttpRequestException or IOException)
        {
            Console.WriteLine($"  the stream broke ({ex.Message}); connecting again");
        }
        await Task.Delay(TimeSpan.FromSeconds(1), ct);
    }
}

var apiKey = Env("REPORT_SERVER_API_KEY");
if (apiKey == "")
{
    Console.Error.WriteLine("Set REPORT_SERVER_API_KEY to an API key (Admin > API keys).");
    return 2;
}
var listen = args is ["events"];
if (args.Length > 0 && !listen)
{
    Console.Error.WriteLine("usage: dotnet run -- [events]");
    return 2;
}
var baseUrl = Env("REPORT_SERVER_URL", "http://localhost:43123").TrimEnd('/') + "/";
using var http = new HttpClient
{
    BaseAddress = new Uri(baseUrl),
    // The event stream stays open; other calls give up after two minutes.
    Timeout = listen ? Timeout.InfiniteTimeSpan : TimeSpan.FromMinutes(2),
};
var server = new ReportServerClient(http, apiKey);
using var cts = new CancellationTokenSource();
Console.CancelKeyPress += (_, e) => { e.Cancel = true; cts.Cancel(); };
try
{
    return listen ? await ListenAsync(server, cts.Token) : await RunAsync(server, cts.Token);
}
catch (ApiException e)
{
    PrintError(e);
    return 1;
}
catch (HttpRequestException e) when (e.StatusCode is null)
{
    Console.Error.WriteLine($"Can't reach {baseUrl}: {e.Message}");
    return 1;
}
catch (OperationCanceledException) when (cts.IsCancellationRequested)
{
    return 130;
}
