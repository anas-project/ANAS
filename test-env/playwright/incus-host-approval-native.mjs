// Real embedded UI + real installed HTTPS/job/hostd behavior in one disposable
// VM. This file never mocks a route, rewrites time, or uses an existing profile.
import { createHash, X509Certificate } from "node:crypto"
import { performance } from "node:perf_hooks"
import { pathToFileURL } from "node:url"
import { chromium } from "@playwright/test"

const origin = "https://anas.native.test:18445"
const schema = "anas.incus-host-browser/v1"
const jobID = /^job_[a-f0-9]{32}$/
const required = ["pinned_test_origin", "embedded_product_assets", "initial_consent_required",
  "actual_expiry_clears_consent", "expiry_never_confirms_or_applies", "fresh_consent_uses_new_plan",
  "browser_job_succeeded", "no_secret_in_storage_or_dom"]

export class BrowserGateFailure extends Error {
  constructor(code) {
    const safe = typeof code === "string" && /^[a-z_]{1,64}$/.test(code) ? code : "browser_gate_failed"
    super(safe)
    this.code = safe
  }
}

function requireGate(condition, code) {
  if (!condition) throw new BrowserGateFailure(code)
}

function exactKeys(value, keys) {
  return value !== null && typeof value === "object" && !Array.isArray(value) &&
    Object.keys(value).sort().join("|") === [...keys].sort().join("|")
}

export function validateInput(input, expectedVM) {
  requireGate(/^anas-incus-host-[a-f0-9]{6}$/.test(expectedVM ?? "") &&
    exactKeys(input, ["schema", "vm_id", "server_spki_sha256", "main_asset_sha256", "session"]) &&
    input.schema === schema && input.vm_id === expectedVM, "browser_input")
  const session = input.session
  requireGate(exactKeys(session, ["schema", "origin", "ca_pem", "session"]) &&
    session.schema === "anas.console-session/v1" && session.origin === origin &&
    typeof session.ca_pem === "string" && session.ca_pem.length < 32768 &&
    session.ca_pem.startsWith("-----BEGIN CERTIFICATE-----\n"), "browser_session")
  requireGate(exactKeys(session.session, ["source", "session_token", "csrf_token"]) && session.session.source === "local" &&
    [session.session.session_token, session.session.csrf_token].every(value =>
      typeof value === "string" && /^[A-Za-z0-9_-]{8,1024}$/.test(value)), "browser_session")
  const pin = Buffer.from(input.server_spki_sha256 ?? "", "base64")
  requireGate(pin.length === 32 && pin.toString("base64") === input.server_spki_sha256 &&
    typeof input.main_asset_sha256 === "string" && /^[a-f0-9]{64}$/.test(input.main_asset_sha256), "browser_identity")
}

export function validateRefresh(evidence) {
  const before = Date.parse(evidence.oldCreatedAt)
  const after = Date.parse(evidence.newCreatedAt)
  requireGate(jobID.test(evidence.before ?? "") && jobID.test(evidence.after ?? "") && evidence.before !== evidence.after &&
    Number.isFinite(before) && Number.isFinite(after) && after - before >= 300_000 &&
    evidence.elapsedMs >= 299_000 && evidence.checkboxChecked === false && evidence.applyDisabled === true &&
    evidence.confirmations === 0 && evidence.applies === 0, "browser_expiry_did_not_reset_consent")
}

async function privateInput(stream) {
  const parts = []
  let size = 0
  for await (const part of stream) {
    const bytes = Buffer.from(part)
    size += bytes.length
    requireGate(size <= 65536, "browser_input_bound")
    parts.push(bytes)
  }
  const raw = Buffer.concat(parts)
  try { return JSON.parse(raw.toString("utf8")) }
  catch { throw new BrowserGateFailure("browser_input_json") }
  finally { raw.fill(0); parts.forEach(part => part.fill(0)) }
}

async function actualJob(page, id) {
  requireGate(jobID.test(id), "browser_job_identity")
  // Browser-origin request with the real HttpOnly cookie. No replacement
  // response, public token, alternative backend or direct root operation.
  const response = await page.evaluate(async id => {
    const response = await fetch(`/api/v1/jobs/${id}`, { credentials: "same-origin", redirect: "error" })
    if (response.status !== 200) return null
    return response.json()
  }, id)
  requireGate(response?.job?.id === id && response.job.workspace_id === "native", "browser_job_readback")
  return response.job
}

async function boundedWait(predicate, timeoutMs, code) {
  const end = performance.now() + timeoutMs
  while (true) {
    const value = await predicate()
    if (value) return value
    requireGate(performance.now() < end, code)
    await new Promise(resolve => setTimeout(resolve, 200))
  }
}

export async function openCertificateEvidence(context, page) {
  const session = await context.newCDPSession(page)
  // Chromium records certificates for this observer only after Network has
  // been enabled. Opening it after navigation can return an empty cache even
  // though TLS succeeded; an empty cache must not be called pin acceptance.
  await session.send("Network.enable")
  return session
}

export async function runBrowser(input, { expectedVM, executablePath }) {
  validateInput(input, expectedVM)
  let browser
  const events = []
  const secrets = [input.session.session.session_token, input.session.session.csrf_token]
  let stage = "pinned_test_origin"
  const record = (name, facts = {}) => {
    const event = { stage: name, status: "passed", ...facts }
    events.push(event)
    process.stdout.write(JSON.stringify(event) + "\n")
  }
  try {
    browser = await chromium.launch({ executablePath, headless: true, timeout: 30_000,
      args: ["--disable-background-networking", "--disable-component-update", "--no-first-run",
        "--host-resolver-rules=MAP anas.native.test 127.0.0.1",
        "--ignore-certificate-errors-spki-list=" + input.server_spki_sha256] })
    const context = await browser.newContext({ locale: "en-US", serviceWorkers: "block" })
    context.setDefaultTimeout(20_000)
    // Isolation also excludes all non-test origins. We never fulfill a route
    // with mock data; same-origin requests reach the installed application.
    await context.route("**/*", async route => {
      const url = new URL(route.request().url())
      if (url.origin !== origin) return route.abort("blockedbyclient")
      return route.continue()
    })
    await context.addCookies([{ name: "__Host-anas_local_session", value: secrets[0], url: origin,
      secure: true, httpOnly: true, sameSite: "Strict" }])
    const page = await context.newPage()
    const cdp = await openCertificateEvidence(context, page)
    const assetBodies = []
    page.on("response", response => {
      if (new URL(response.url()).pathname.endsWith("/assets/main.js")) {
        assetBodies.push(response.body().then(body => createHash("sha256").update(body).digest("hex")).catch(() => null))
      }
    })
    const calls = { plans: 0, confirmations: 0, applies: 0 }
    page.on("request", request => {
      if (request.method() !== "POST") return
      const path = new URL(request.url()).pathname
      if (path === "/api/v1/workspaces/native/host/actions/incus/uninstall/plan") calls.plans++
      if (path === "/api/v1/workspaces/native/host/actions/confirm") calls.confirmations++
      if (path === "/api/v1/workspaces/native/host/actions/incus/uninstall/apply") calls.applies++
    })
    const response = await page.goto(origin + "/#/maintenance", { waitUntil: "networkidle", timeout: 30_000 })
    requireGate(response?.status() === 200 && page.url() === origin + "/#/maintenance", "browser_origin")
    // The browser's only self-signed exception is the exact installed SPKI.
    // Independently read its actual TLS certificate from the CDP connection.
    const certificate = await cdp.send("Network.getCertificate", { origin })
    requireGate(Array.isArray(certificate.tableNames) && certificate.tableNames.length > 0, "browser_tls_evidence")
    const leaf = new X509Certificate(Buffer.from(certificate.tableNames[0], "base64"))
    const actualPin = createHash("sha256").update(leaf.publicKey.export({ type: "spki", format: "der" })).digest("base64")
    requireGate(actualPin === input.server_spki_sha256, "browser_tls_pin")
    await cdp.detach()
    record(stage, { browser_version: browser.version() })

    stage = "embedded_product_assets"
    await page.locator(".incus-actions").waitFor({ state: "visible" })
    requireGate(assetBodies.length === 1 && (await assetBodies[0]) === input.main_asset_sha256, "browser_asset_digest")
    record(stage)

    stage = "initial_consent_required"
    const component = page.locator(".incus-actions")
    requireGate(await component.locator('input[type="password"]').count() === 0, "browser_root_password_field")
    await page.locator("[data-workspace-maintenance] .workspace-picker select").selectOption("native")
    await component.locator(".maintenance-form-row select").first().selectOption("uninstall")
    await component.locator('input[type="number"]').fill("16")
    const started = performance.now()
    await component.locator(".maintenance-form-row button").first().click()
    const preview = component.locator(":scope > .plan-preview").filter({ has: page.locator("p > code") }).first()
    await preview.locator("p > code").first().waitFor({ state: "visible" })
    const oldID = (await preview.locator("p > code").first().textContent())?.trim()
    requireGate(jobID.test(oldID ?? ""), "browser_old_plan")
    const oldJob = await actualJob(page, oldID)
    requireGate(oldJob.status === "succeeded" && oldJob.mutating === false, "browser_old_plan_result")
    const checkbox = preview.locator('input[type="checkbox"]').first()
    const apply = preview.locator("button.primary-button").first()
    requireGate(!await checkbox.isChecked() && await apply.isDisabled(), "browser_implicit_consent")
    await checkbox.check()
    requireGate(await checkbox.isChecked() && !await apply.isDisabled(), "browser_consent_not_bound")
    record(stage, { plan_job_id: oldID })

    stage = "actual_expiry_clears_consent"
    process.stdout.write(JSON.stringify({ stage, status: "waiting_for_actual_expiry" }) + "\n")
    const newID = await boundedWait(async () => {
      const codes = component.locator(":scope > .plan-preview p > code")
      if (await codes.count() !== 1) return null
      const id = (await codes.first().textContent())?.trim()
      return jobID.test(id ?? "") && id !== oldID ? id : null
    }, 335_000, "browser_plan_did_not_refresh")
    const newJob = await actualJob(page, newID)
    const evidence = { before: oldID, after: newID, oldCreatedAt: oldJob.created_at, newCreatedAt: newJob.created_at,
      elapsedMs: Math.floor(performance.now() - started), checkboxChecked: await checkbox.isChecked(),
      applyDisabled: await apply.isDisabled(), confirmations: calls.confirmations, applies: calls.applies }
    validateRefresh(evidence)
    record(stage, { old_plan_job_id: oldID, new_plan_job_id: newID, elapsed_ms: evidence.elapsedMs })
    stage = "expiry_never_confirms_or_applies"
    requireGate(calls.plans === 2 && calls.confirmations === 0 && calls.applies === 0, "browser_expiry_triggered_write")
    record(stage)

    stage = "fresh_consent_uses_new_plan"
    await checkbox.check()
    const confirmationResponse = page.waitForResponse(response => response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/v1/workspaces/native/host/actions/confirm")
    const applyResponse = page.waitForResponse(response => response.request().method() === "POST" &&
      new URL(response.url()).pathname === "/api/v1/workspaces/native/host/actions/incus/uninstall/apply")
    await apply.click()
    const proof = await confirmationResponse
    requireGate(proof.status() === 201 && proof.request().postDataJSON()?.plan_job_id === newID, "browser_confirmation_plan")
    const confirmation = await proof.json()
    requireGate(typeof confirmation.token === "string" && confirmation.token.length > 0, "browser_confirmation")
    secrets.push(confirmation.token)
    const submitted = await applyResponse
    const payload = submitted.request().postDataJSON()
    requireGate(submitted.status() === 202 && payload?.plan_job_id === newID && payload?.confirmation_token === confirmation.token &&
      calls.confirmations === 1 && calls.applies === 1, "browser_apply_plan")
    const queued = await submitted.json()
    const id = queued?.job?.id
    requireGate(jobID.test(id ?? ""), "browser_apply_job")
    record(stage, { plan_job_id: newID, apply_job_id: id })

    stage = "browser_job_succeeded"
    const job = await boundedWait(async () => {
      const job = await actualJob(page, id)
      return ["queued", "running"].includes(job.status) ? null : job
    }, 60_000, "browser_apply_timeout")
    requireGate(job.status === "succeeded" && !job.needs_compensation_check &&
      job.result?.value?.disposition === "uninstalled" && job.result?.value?.compute_ready === false,
      "browser_apply_failed")
    record(stage, { job_id: id })

    stage = "no_secret_in_storage_or_dom"
    const persisted = await page.evaluate(() => ({ local: { ...localStorage }, session: { ...sessionStorage },
      text: document.body.innerText, location: location.href }))
    const text = JSON.stringify(persisted)
    requireGate(!text.includes("PRIVATE KEY-----") && secrets.every(value => !text.includes(value)), "browser_secret_exposure")
    record(stage)
    return { schema, vm_id: expectedVM, passed: events.length === required.length &&
      required.every(name => events.filter(event => event.stage === name && event.status === "passed").length === 1),
      scope: "actual embedded UI, HTTPS, natural plan expiry and explicit renewed consent; not all browser/product workflows", events }
  } catch (error) {
    events.push({ stage, status: "failed", code: error instanceof BrowserGateFailure ? error.code : "browser_gate_failed" })
    return { schema, vm_id: expectedVM, passed: false, events }
  } finally {
    secrets.fill("")
    if (browser) await browser.close()
  }
}

async function main() {
  const args = process.argv.slice(2)
  requireGate(args.length === 4 && args[0] === "--vm-id" && args[2] === "--browser-executable", "browser_arguments")
  const input = await privateInput(process.stdin)
  const result = await runBrowser(input, { expectedVM: args[1], executablePath: args[3] })
  process.stdout.write(JSON.stringify(result) + "\n")
  process.exitCode = result.passed ? 0 : 1
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch(error => {
    process.stdout.write(JSON.stringify({ schema, passed: false,
      code: error instanceof BrowserGateFailure ? error.code : "browser_gate_failed" }) + "\n")
    process.exitCode = 1
  })
}
