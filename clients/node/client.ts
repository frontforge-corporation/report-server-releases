// The few calls an application makes to Frontforge Report Server, over
// Node's built-in fetch.

import { randomBytes } from "node:crypto"
import { setTimeout as sleep } from "node:timers/promises"

export interface Diagnostic {
  code: string
  severity: "error" | "warning" | "info"
  message: string
  node?: string
  path?: string
}

/**
 * An answer that wasn't 2xx. The server answers errors as JSON:
 * {"error": <kind>, "message": ...}, and a render the engine refused adds
 * "diagnostics", each with code, severity, message and where in the
 * template it is.
 */
export class ApiError extends Error {
  readonly status: number
  readonly kind: string
  readonly diagnostics: Diagnostic[]
  readonly requestId: string | null
  readonly retryAfter: number | null

  constructor(status: number, kind: string, message: string, diagnostics: Diagnostic[], requestId: string | null, retryAfter: number | null) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.kind = kind
    this.diagnostics = diagnostics
    this.requestId = requestId
    this.retryAfter = retryAfter
  }

  static async from(res: Response): Promise<ApiError> {
    const text = await res.text()
    let body: { error?: string; message?: string; diagnostics?: Diagnostic[] } = {}
    try {
      body = JSON.parse(text)
    } catch {
      body = { message: text.trim() }
    }
    const retry = Number(res.headers.get("Retry-After"))
    return new ApiError(
      res.status,
      body.error || `http-${res.status}`,
      body.message || res.statusText,
      body.diagnostics ?? [],
      res.headers.get("X-Request-Id"),
      Number.isFinite(retry) && retry > 0 ? retry : null,
    )
  }
}

export interface Me {
  kind: string
  name: string
  permissions: string[]
}

export interface Template {
  id: string
  name: string
  folder: string
  /** Each environment the template is published in, with its newest version there. */
  published: Record<string, { commit: string; at: string }>
}

/** A job's run: its datasets by name, and its params. */
export interface Run {
  datasets?: Record<string, unknown>
  params?: Record<string, unknown>
}

export interface Job {
  id: string
  status: "queued" | "running" | "done" | "error" | "cancelled"
  error?: string
  reportIds?: string[]
  progress: { done: number; total: number }
}

export interface RenderRequest {
  templateId: string
  environment?: string
  format?: "pdf" | "docx" | "xlsx" | "html-paged" | "html-flow" | "png" | "svg"
  datasets?: Record<string, unknown>
  params?: Record<string, unknown>
  options?: Record<string, unknown>
}

export interface Rendered {
  body: Uint8Array
  /** X-Report-Id: a PDF render is stored, and can be downloaded again. */
  reportId: string | null
  pages: string | null
  /** X-Template-Commit: the published version that was used. */
  commit: string | null
  /** The render's warnings, such as data that doesn't match the template's schema: X-Rfml-Diagnostics, JSON in unpadded base64url. */
  diagnostics: Diagnostic[]
}

export interface ServerEvent {
  name: string
  data: unknown
}

export class ReportServer {
  readonly baseUrl: string
  readonly apiKey: string

  constructor(baseUrl: string, apiKey: string) {
    this.baseUrl = baseUrl.replace(/\/+$/, "")
    this.apiKey = apiKey
  }

  /**
   * Sends a request, waits out a rate limit (429) up to three times, and
   * throws an ApiError for any other answer that isn't 2xx.
   */
  async request(method: string, path: string, body?: unknown, headers: Record<string, string> = {}, signal?: AbortSignal): Promise<Response> {
    for (let attempt = 0; ; attempt++) {
      const res = await fetch(this.baseUrl + path, {
        method,
        signal: signal ?? AbortSignal.timeout(120_000),
        headers: {
          // An API key goes in the Authorization header as a bearer token.
          Authorization: `Bearer ${this.apiKey}`,
          // Our own request id, which the server logs and sends back: quote
          // it to whoever runs the server when something goes wrong.
          "X-Request-Id": randomBytes(8).toString("hex"),
          ...(body === undefined ? {} : { "Content-Type": "application/json" }),
          ...headers,
        },
        body: body === undefined ? undefined : JSON.stringify(body),
      })
      if (res.ok) return res
      const err = await ApiError.from(res)
      if (res.status !== 429 || attempt === 3) throw err
      const wait = Math.min(err.retryAfter ?? 1, 30)
      console.log(`  rate limited; trying again in ${wait} s`)
      await sleep(wait * 1000)
    }
  }

  async json<T>(method: string, path: string, body?: unknown, headers?: Record<string, string>): Promise<T> {
    return (await (await this.request(method, path, body, headers)).json()) as T
  }

  me(): Promise<Me> {
    return this.json("GET", "/v1/me")
  }

  async templates(): Promise<Template[]> {
    return (await this.json<{ templates: Template[] }>("GET", "/v1/templates")).templates
  }

  /** Renders a published template straight away. */
  async render(req: RenderRequest): Promise<Rendered> {
    const res = await this.request("POST", "/v1/render", req)
    const diagnostics = res.headers.get("X-Rfml-Diagnostics")
    return {
      body: new Uint8Array(await res.arrayBuffer()),
      reportId: res.headers.get("X-Report-Id"),
      pages: res.headers.get("X-Rfml-Pages"),
      commit: res.headers.get("X-Template-Commit"),
      diagnostics: diagnostics ? (JSON.parse(Buffer.from(diagnostics, "base64url").toString("utf8")) as Diagnostic[]) : [],
    }
  }

  /**
   * Queues a background render, one render per run. The same idempotency
   * key with the same request gets the same job back, so a submission that
   * timed out can be sent again safely.
   */
  submitJob(templateId: string, environment: string | undefined, runs: Run[], idempotencyKey: string): Promise<Job> {
    return this.json("POST", "/v1/jobs", { templateId, environment, format: "pdf", runs }, { "Idempotency-Key": idempotencyKey })
  }

  job(id: string): Promise<Job> {
    return this.json("GET", `/v1/jobs/${encodeURIComponent(id)}`)
  }

  /**
   * Polls the job once a second until it is done, error or cancelled. A job
   * can instead call your application back when it finishes: see
   * callbackUrl in the API reference.
   */
  async waitForJob(id: string, progress: (job: Job) => void, timeoutMs = 300_000): Promise<Job> {
    const deadline = Date.now() + timeoutMs
    for (;;) {
      const job = await this.job(id)
      progress(job)
      if (job.status === "done" || job.status === "error" || job.status === "cancelled") return job
      if (Date.now() > deadline) throw new Error(`job ${id} is still ${job.status} after ${timeoutMs / 1000} s`)
      await sleep(1000)
    }
  }

  /** Reads a stored report. */
  async download(reportId: string): Promise<Uint8Array> {
    const res = await this.request("GET", `/v1/reports/${encodeURIComponent(reportId)}`)
    return new Uint8Array(await res.arrayBuffer())
  }

  /**
   * GET /v1/events as events, until the stream ends. An event is "event:"
   * and "data:" lines ended by a blank line; lines starting with ":" are
   * keep-alive comments (every 25 s).
   */
  async *events(signal?: AbortSignal): AsyncGenerator<ServerEvent> {
    const res = await this.request("GET", "/v1/events", undefined, { Accept: "text/event-stream" }, signal ?? new AbortController().signal)
    if (!res.body) return
    let buffer = ""
    let name = "message"
    let data: string[] = []
    for await (const chunk of res.body.pipeThrough(new TextDecoderStream())) {
      buffer += chunk
      let nl: number
      while ((nl = buffer.indexOf("\n")) >= 0) {
        const line = buffer.slice(0, nl).replace(/\r$/, "")
        buffer = buffer.slice(nl + 1)
        if (line === "") {
          if (data.length > 0) yield { name, data: JSON.parse(data.join("\n")) }
          name = "message"
          data = []
        } else if (!line.startsWith(":")) {
          const colon = line.indexOf(":")
          const field = colon < 0 ? line : line.slice(0, colon)
          const value = colon < 0 ? "" : line.slice(colon + 1).replace(/^ /, "")
          if (field === "event") name = value
          else if (field === "data") data.push(value)
        }
      }
    }
  }
}
