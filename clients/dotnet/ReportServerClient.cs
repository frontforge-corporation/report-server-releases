// The few calls an application makes to Frontforge Report Server, over
// HttpClient and System.Text.Json.

using System.Buffers.Text;
using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Runtime.CompilerServices;
using System.Security.Cryptography;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.Json.Serialization;

namespace ReportClient;

public sealed record Diagnostic(string Code, string Severity, string Message, string? Node, string? Path);

/// <summary>
/// An answer that wasn't 2xx. The server answers errors as JSON:
/// {"error": kind, "message": ...}, and a render the engine refused adds
/// "diagnostics", each with code, severity, message and where in the
/// template it is.
/// </summary>
public sealed class ApiException(HttpStatusCode status, string kind, string message, IReadOnlyList<Diagnostic> diagnostics, string? requestId, TimeSpan? retryAfter)
    : Exception(message)
{
    public HttpStatusCode Status { get; } = status;
    public string Kind { get; } = kind;
    public IReadOnlyList<Diagnostic> Diagnostics { get; } = diagnostics;
    public string? RequestId { get; } = requestId;
    public TimeSpan? RetryAfter { get; } = retryAfter;

    private sealed record Body(string? Error, string? Message, List<Diagnostic>? Diagnostics);

    internal static async Task<ApiException> FromAsync(HttpResponseMessage res, CancellationToken ct)
    {
        var text = await res.Content.ReadAsStringAsync(ct);
        Body? body = null;
        try { body = JsonSerializer.Deserialize<Body>(text, ReportServerClient.Json); }
        catch (JsonException) { }
        var status = (int)res.StatusCode;
        return new ApiException(
            res.StatusCode,
            string.IsNullOrEmpty(body?.Error) ? $"http-{status}" : body.Error,
            !string.IsNullOrEmpty(body?.Message) ? body.Message : body is null && text.Trim() != "" ? text.Trim() : res.ReasonPhrase ?? "",
            body?.Diagnostics ?? [],
            res.Headers.TryGetValues("X-Request-Id", out var ids) ? ids.FirstOrDefault() : null,
            res.Headers.RetryAfter?.Delta);
    }
}

public sealed record Me(string Kind, string Name, List<string> Permissions);

public sealed record Published(string Commit, DateTimeOffset At);

/// <summary>An entry of GET /v1/templates. Published maps each environment the template is published in to its newest version there.</summary>
public sealed record Template(string Id, string Name, string Folder, Dictionary<string, Published> Published);

/// <summary>A job's run: its datasets by name, and its params.</summary>
public sealed record Run(JsonObject? Datasets, JsonObject? Params = null);

public sealed record JobProgress(int Done, int Total);

public sealed record Job(string Id, string Status, string? Error, List<string>? ReportIds, JobProgress Progress)
{
    public bool Finished => Status is "done" or "error" or "cancelled";
}

public sealed record RenderRequest(
    string TemplateId,
    string? Environment = null,
    string Format = "pdf",
    JsonObject? Datasets = null,
    JsonObject? Params = null,
    JsonObject? Options = null);

/// <summary>A render's bytes, and what the server says about them.</summary>
/// <param name="ReportId">X-Report-Id: a PDF render is stored, and can be downloaded again.</param>
/// <param name="Commit">X-Template-Commit: the published version that was used.</param>
/// <param name="Diagnostics">The render's warnings, such as data that doesn't match the template's schema: X-Rfml-Diagnostics, JSON in unpadded base64url.</param>
public sealed record Rendered(byte[] Body, string? ReportId, string? Pages, string? Commit, IReadOnlyList<Diagnostic> Diagnostics);

public sealed record ServerEvent(string Name, JsonNode? Data);

public sealed class ReportServerClient
{
    internal static readonly JsonSerializerOptions Json = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.WhenWritingNull,
    };

    private readonly HttpClient http;

    /// <param name="http">Its BaseAddress is the server; give it an infinite Timeout and pass a token per call, since the event stream stays open.</param>
    public ReportServerClient(HttpClient http, string apiKey)
    {
        this.http = http;
        // An API key goes in the Authorization header as a bearer token.
        http.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", apiKey);
    }

    /// <summary>
    /// Sends a request, waits out a rate limit (429) up to three times, and
    /// throws an ApiException for any other answer that isn't 2xx.
    /// </summary>
    private async Task<HttpResponseMessage> SendAsync(Func<HttpRequestMessage> make, CancellationToken ct,
        HttpCompletionOption completion = HttpCompletionOption.ResponseContentRead)
    {
        for (var attempt = 0; ; attempt++)
        {
            using var req = make();
            // Our own request id, which the server logs and sends back: quote
            // it to whoever runs the server when something goes wrong.
            req.Headers.Add("X-Request-Id", Convert.ToHexStringLower(RandomNumberGenerator.GetBytes(8)));
            var res = await http.SendAsync(req, completion, ct);
            if (res.IsSuccessStatusCode) return res;
            using (res)
            {
                var err = await ApiException.FromAsync(res, ct);
                if (res.StatusCode != HttpStatusCode.TooManyRequests || attempt == 3) throw err;
                var wait = TimeSpan.FromSeconds(Math.Clamp((err.RetryAfter ?? TimeSpan.FromSeconds(1)).TotalSeconds, 1, 30));
                Console.WriteLine($"  rate limited; trying again in {wait.TotalSeconds} s");
                await Task.Delay(wait, ct);
            }
        }
    }

    private async Task<T> JsonAsync<T>(HttpMethod method, string path, object? body, CancellationToken ct, (string, string)? header = null)
    {
        using var res = await SendAsync(() =>
        {
            var req = new HttpRequestMessage(method, path);
            if (body is not null) req.Content = JsonContent.Create(body, options: Json);
            if (header is var (name, value)) req.Headers.Add(name, value);
            return req;
        }, ct);
        return await res.Content.ReadFromJsonAsync<T>(Json, ct) ?? throw new InvalidDataException($"{path} answered null");
    }

    public Task<Me> MeAsync(CancellationToken ct = default) => JsonAsync<Me>(HttpMethod.Get, "v1/me", null, ct);

    private sealed record TemplateList(List<Template> Templates);

    public async Task<List<Template>> TemplatesAsync(CancellationToken ct = default) =>
        (await JsonAsync<TemplateList>(HttpMethod.Get, "v1/templates", null, ct)).Templates;

    /// <summary>Renders a published template straight away.</summary>
    public async Task<Rendered> RenderAsync(RenderRequest request, CancellationToken ct = default)
    {
        using var res = await SendAsync(() => new HttpRequestMessage(HttpMethod.Post, "v1/render")
        {
            Content = JsonContent.Create(request, options: Json),
        }, ct);
        string? Header(string name) => res.Headers.TryGetValues(name, out var v) ? v.FirstOrDefault() : null;
        var diagnostics = Header("X-Rfml-Diagnostics") is { } h
            ? JsonSerializer.Deserialize<List<Diagnostic>>(Base64Url.DecodeFromChars(h), Json) ?? []
            : [];
        return new Rendered(await res.Content.ReadAsByteArrayAsync(ct), Header("X-Report-Id"), Header("X-Rfml-Pages"), Header("X-Template-Commit"), diagnostics);
    }

    /// <summary>
    /// Queues a background render, one render per run. The same idempotency
    /// key with the same request gets the same job back, so a submission that
    /// timed out can be sent again safely.
    /// </summary>
    public Task<Job> SubmitJobAsync(string templateId, string? environment, IEnumerable<Run> runs, string idempotencyKey, CancellationToken ct = default) =>
        JsonAsync<Job>(HttpMethod.Post, "v1/jobs", new { templateId, environment, format = "pdf", runs }, ct, ("Idempotency-Key", idempotencyKey));

    public Task<Job> JobAsync(string id, CancellationToken ct = default) =>
        JsonAsync<Job>(HttpMethod.Get, $"v1/jobs/{Uri.EscapeDataString(id)}", null, ct);

    /// <summary>
    /// Polls the job once a second until it has finished. A job can instead
    /// call your application back when it finishes: see callbackUrl in the
    /// API reference.
    /// </summary>
    public async Task<Job> WaitForJobAsync(string id, Action<Job> progress, CancellationToken ct = default)
    {
        while (true)
        {
            var job = await JobAsync(id, ct);
            progress(job);
            if (job.Finished) return job;
            await Task.Delay(TimeSpan.FromSeconds(1), ct);
        }
    }

    /// <summary>Reads a stored report.</summary>
    public async Task<byte[]> DownloadAsync(string reportId, CancellationToken ct = default)
    {
        using var res = await SendAsync(() => new HttpRequestMessage(HttpMethod.Get, $"v1/reports/{Uri.EscapeDataString(reportId)}"), ct);
        return await res.Content.ReadAsByteArrayAsync(ct);
    }

    /// <summary>
    /// GET /v1/events, until the stream ends. An event is "event:" and
    /// "data:" lines ended by a blank line; lines starting with ":" are
    /// keep-alive comments (every 25 s).
    /// </summary>
    public async IAsyncEnumerable<ServerEvent> EventsAsync([EnumeratorCancellation] CancellationToken ct = default)
    {
        using var res = await SendAsync(() =>
        {
            var req = new HttpRequestMessage(HttpMethod.Get, "v1/events");
            req.Headers.Accept.Add(new MediaTypeWithQualityHeaderValue("text/event-stream"));
            return req;
        }, ct, HttpCompletionOption.ResponseHeadersRead);
        using var reader = new StreamReader(await res.Content.ReadAsStreamAsync(ct));
        var name = "message";
        var data = new List<string>();
        while (await reader.ReadLineAsync(ct) is { } line)
        {
            if (line.Length == 0)
            {
                if (data.Count > 0) yield return new ServerEvent(name, JsonNode.Parse(string.Join('\n', data)));
                name = "message";
                data.Clear();
            }
            else if (!line.StartsWith(':'))
            {
                var colon = line.IndexOf(':');
                var field = colon < 0 ? line : line[..colon];
                var value = colon < 0 ? "" : line[(colon + 1)..];
                if (value.StartsWith(' ')) value = value[1..];
                if (field == "event") name = value;
                else if (field == "data") data.Add(value);
            }
        }
    }
}
