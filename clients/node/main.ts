// A sample client for the Frontforge Report Server HTTP API.
//
// It authenticates with an API key, lists the templates, renders one PDF
// straight away (POST /v1/render), renders a batch in the background
// (POST /v1/jobs), waits for that job and downloads what it made, and shows
// what an error looks like. `node main.ts events` listens to GET /v1/events
// instead.
//
// No dependencies at run time (TypeScript only checks the types). It is
// configured by environment variables: see README.md.

import { randomUUID } from "node:crypto"
import { mkdir, readFile, writeFile } from "node:fs/promises"
import { join } from "node:path"
import { setTimeout as sleep } from "node:timers/promises"
import { ApiError, ReportServer, type Template } from "./client.ts"

const env = (name: string, fallback = ""): string => process.env[name]?.trim() || fallback

function printError(e: ApiError): void {
  console.log(`  ${e.status} ${e.kind}: ${e.message}`)
  if (e.requestId) console.log(`  request id ${e.requestId}`)
  const errors = e.diagnostics.filter((d) => d.severity === "error").slice(0, 5)
  for (const d of errors) console.log(`  - ${d.code} at ${d.node || d.path || ""}: ${d.message}`)
  const rest = e.diagnostics.length - errors.length
  if (rest > 0) console.log(`  ... and ${rest} more diagnostics (warnings included)`)
}

// The gallery example letters.json is for. A report copied from it keeps
// this name, but not always one id: New report in the studio makes the id
// from the title (letter-to-customers), while an empty catalog's "Start
// from an example" keeps the example's own (customer-letter), and either
// ends up with another when that one is taken. GET /v1/templates doesn't say
// which example a template came from, so without REPORT_TEMPLATE the
// samples look for this name.
const EXAMPLE_NAME = "Letter to customers"

/** The template to render, REPORT_TEMPLATE's or else the one published as EXAMPLE_NAME, and a line to print; or "" and why not. */
function pickTemplate(templates: Template[], wanted: string, environment: string | undefined): [string, string] {
  const where = environment ? ` in ${environment}` : ""
  const published = (t: Template) => (environment ? environment in t.published : Object.keys(t.published).length > 0)
  if (wanted) {
    const t = templates.find((t) => t.id === wanted)
    if (!t) return ["", `There's no template ${wanted}. Set REPORT_TEMPLATE to one of the ids above.`]
    if (!published(t)) return ["", `${wanted} isn't published${where}. Publish it in the studio.`]
    return [wanted, `Using ${wanted}, from REPORT_TEMPLATE.`]
  }
  const named = templates.filter((t) => t.name.trim().toLowerCase() === EXAMPLE_NAME.toLowerCase())
  if (!named.length) {
    return ["", `No template is called ${EXAMPLE_NAME}. In the studio, choose New report, then From a template, pick ${EXAMPLE_NAME} and publish it; or set REPORT_TEMPLATE to another template's id.`]
  }
  const live = named.filter(published)
  if (!live.length) {
    const ids = named.map((t) => t.id).join(", ")
    return ["", `No template called ${EXAMPLE_NAME} is published${where} (${ids}). Publish it in the studio, or set REPORT_TEMPLATE.`]
  }
  if (live.length > 1) {
    return [live[0].id, `Using ${live[0].id}, the first of ${live.length} published templates called ${EXAMPLE_NAME}. Set REPORT_TEMPLATE to choose another.`]
  }
  return [live[0].id, `Using ${live[0].id}, the published template called ${EXAMPLE_NAME}.`]
}

async function run(server: ReportServer): Promise<number> {
  const environment = env("REPORT_ENVIRONMENT") || undefined
  const out = env("REPORT_OUT", "out")
  // One element per run: each is that run's datasets, by dataset name.
  const runs = JSON.parse(await readFile(env("REPORT_DATA", "letters.json"), "utf8")) as Record<string, unknown>[]

  const me = await server.me()
  console.log(`Authenticated as ${me.kind} ${me.name}: ${me.permissions.join(", ")}`)

  console.log("\nTemplates:")
  const templates = await server.templates()
  for (const t of templates) {
    const where = Object.keys(t.published).sort().join(", ") || "not published"
    console.log(`  ${t.id.padEnd(24)} ${t.name} (${where})`)
  }
  const [templateId, line] = pickTemplate(templates, env("REPORT_TEMPLATE"), environment)
  console.log(`\n${line}`)
  if (!templateId) return 1
  await mkdir(out, { recursive: true })

  console.log(`\n1. Rendering ${templateId} now (POST /v1/render)`)
  const r = await server.render({ templateId, environment, format: "pdf", datasets: runs[0] })
  let path = join(out, `${templateId}.pdf`)
  await writeFile(path, r.body)
  console.log(`  wrote ${path}: ${r.body.length} bytes, ${r.pages} page(s), report ${r.reportId}, template commit ${r.commit?.slice(0, 8)}`)
  // A render that succeeds can still have warnings, such as data that
  // doesn't match the template's schema.
  for (const d of r.diagnostics) console.log(`  ${d.severity} ${d.code}: ${d.message}`)

  console.log(`\n2. Rendering ${runs.length} in the background (POST /v1/jobs)`)
  let job = await server.submitJob(templateId, environment, runs.map((datasets) => ({ datasets })), randomUUID())
  console.log(`  job ${job.id} is ${job.status}`)
  let seen = ""
  job = await server.waitForJob(job.id, (j) => {
    const s = `${j.status}: ${j.progress.done} of ${j.progress.total} rendered`
    if (s !== seen) console.log(`  ${(seen = s)}`)
  })
  if (job.status !== "done") {
    console.log(`  the job ended ${job.status}: ${job.error ?? ""}`)
    return 1
  }
  for (const [n, id] of (job.reportIds ?? []).entries()) {
    let pdf: Uint8Array
    try {
      pdf = await server.download(id)
    } catch (e) {
      if (e instanceof ApiError && e.status === 403) {
        console.log("  the key may render but not download stored reports: give it report.read (see README.md)")
        return 1
      }
      throw e
    }
    path = join(out, `${templateId}-${n + 1}.pdf`)
    await writeFile(path, pdf)
    console.log(`  wrote ${path}: ${pdf.length} bytes`)
  }

  // By default, data that breaks the template's schema is only a warning,
  // and what can't be bound renders empty; strict makes it an error.
  console.log("\n3. Rendering empty data in strict schema mode, to show an error")
  const empty = Object.fromEntries(Object.keys(runs[0]).map((name) => [name, {}]))
  try {
    await server.render({ templateId, environment, datasets: empty, options: { schemaMode: "strict" } })
    console.log("  it rendered anyway")
  } catch (e) {
    if (!(e instanceof ApiError)) throw e
    printError(e)
  }
  return 0
}

// Prints the server's events until the sign-in ends or Ctrl+C. When the
// server restarts, the stream ends after a `restarting` event: connect
// again, as a browser's EventSource does by itself.
async function listen(server: ReportServer): Promise<number> {
  console.log("Listening to GET /v1/events; Ctrl+C stops.")
  for (;;) {
    try {
      for await (const e of server.events()) {
        console.log(`  ${e.name}: ${JSON.stringify(e.data)}`)
        if (e.name === "signed-out") return 0
      }
    } catch (e) {
      if (e instanceof ApiError) throw e
      console.log(`  the stream broke (${(e as Error).message}); connecting again`)
    }
    await sleep(1000)
  }
}

async function main(args: string[]): Promise<number> {
  const apiKey = env("REPORT_SERVER_API_KEY")
  if (!apiKey) {
    console.error("Set REPORT_SERVER_API_KEY to an API key (Admin > API keys).")
    return 2
  }
  const server = new ReportServer(env("REPORT_SERVER_URL", "http://localhost:43123"), apiKey)
  try {
    if (args.length === 1 && args[0] === "events") return await listen(server)
    if (args.length > 0) {
      console.error("usage: node main.ts [events]")
      return 2
    }
    return await run(server)
  } catch (e) {
    if (e instanceof ApiError) {
      printError(e)
      return 1
    }
    if (e instanceof TypeError && e.cause) {
      console.error(`Can't reach ${server.baseUrl}: ${(e.cause as Error).message}`)
      return 1
    }
    throw e
  }
}

process.exitCode = await main(process.argv.slice(2))
