// REQUIREMENTS: HOSTACT-R-009 HOSTACT-R-010 HOSTACT-R-011
// Local component/browser fixture only. All API calls use in-memory responses;
// no authentication store, privileged socket, host action or network is changed.
import assert from "node:assert/strict"
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises"
import { createRequire } from "node:module"
import { tmpdir } from "node:os"
import path from "node:path"
import { fileURLToPath } from "node:url"

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..")
const web = path.join(root, "web")
const webRequire = createRequire(path.join(web, "package.json"))
const rootRequire = createRequire(path.join(root, "package.json"))
const { createServer } = await import(webRequire.resolve("vite"))
const { default: vue } = await import(webRequire.resolve("@vitejs/plugin-vue"))
const { chromium } = rootRequire("playwright")
const temp = await mkdtemp(path.join(tmpdir(), "anas-incus-ui-"))
const reports = path.join(root, "test-env/reports/incus-approval-ui")
const channel = process.env.ANAS_UI_BROWSER_CHANNEL || undefined
assert(channel === undefined || channel === "chrome" || channel === "chromium", "unsupported fixture browser channel")
let server, browser
try {
  await mkdir(reports, { recursive: true })
  await writeFile(path.join(temp, "index.html"), '<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"></head><body><div id="app"></div><script type="module" src="/entry.js"></script></body></html>')
  await writeFile(path.join(temp, "entry.js"), `import {createApp,h} from "vue";
import Incus from ${JSON.stringify(path.join(web, "src/maintenance/IncusHostActions.vue"))};
import ${JSON.stringify(path.join(web, "src/styles.css"))};
createApp({render:()=>h(Incus,{workspace:"main",csrf:"fixture-csrf",locale:"zh",disabled:false})}).mount("#app");`)
  server = await createServer({ configFile: false, root: temp, plugins: [vue()],
    resolve: { alias: { vue: webRequire.resolve("vue/dist/vue.runtime.esm-bundler.js") } },
    server: { host: "127.0.0.1", port: 0, fs: { allow: [temp, root] } }, logLevel: "error" })
  await server.listen()
  const address = server.httpServer.address()
  assert(address && typeof address === "object")
  browser = await chromium.launch({ headless: true, channel })
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  const errors = []
  page.on("pageerror", (error) => errors.push(error.message))
  let planned, confirms = 0, applies = 0
  const digest = "a".repeat(64)
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    let body
    if (url.pathname.endsWith("/install/plan")) {
      const input = request.postDataJSON()
      const created = new Date().toISOString()
      const parameters = { schema: "anas.host-action.incus/v1", request: input.request,
        binding: { schema: "anas.incus-host-provision/v1", phase: "install", plan_digest: digest, destructive: true } }
      planned = { api_version: "anas.dev/api/v1", job: { id: "plan-ui-1", kind: "action", workspace_id: "main", mutating: false,
        status: "succeeded", created_at: created, progress: 100, revision: 2, warnings: [], needs_compensation_check: false,
        result: { changed: false, value: { schema: "anas.host-action.incus/v1", action: "incus.install", phase: "install", parameters,
          inspect: { schema: "anas.incus-host-provision/v1", plan: { schema: "anas.incus-host-provision/v1", digest,
            steps: [{ id: "packages", phase: "install", effect: "安装已批准的官方软件包", destructive: true }], blockers: [] } } } } } }
      body = planned
    } else if (url.pathname.endsWith("/confirm")) {
      assert.equal(request.postDataJSON().plan_job_id, "plan-ui-1")
      confirms++
      body = { token: "fixture-only-proof", binding_digest: digest, expires_at: new Date(Date.parse(planned.job.created_at) + 300000).toISOString() }
    } else if (url.pathname.endsWith("/install/apply")) {
      const input = request.postDataJSON()
      assert.equal(input.plan_job_id, "plan-ui-1")
      assert.equal(input.confirmation_token, "fixture-only-proof")
      assert.deepEqual(input.parameters, planned.job.result.value.parameters)
      applies++
      body = { ...planned, job: { ...planned.job, id: "apply-ui-1", mutating: true, status: "queued" } }
    } else {
      throw new Error("Unexpected API request in isolated component fixture")
    }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) })
  })
  await page.goto(`http://127.0.0.1:${address.port}/`)
  await page.getByRole("button", { name: "创建计划任务", exact: true }).click()
  await page.getByRole("heading", { name: "确认宿主计划", exact: true }).waitFor()
  assert.equal(await page.locator('input[type="password"]').count(), 0)
  const execute = page.getByRole("button", { name: "创建执行任务", exact: true })
  assert(await execute.isDisabled())
  assert.equal(confirms, 0)
  for (const [name, width, height] of [["desktop", 1280, 900], ["mobile", 390, 844]]) {
    await page.setViewportSize({ width, height })
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), `${name} horizontal overflow`)
    await page.screenshot({ path: path.join(reports, `${name}.png`), fullPage: true })
  }
  await page.getByRole("checkbox").check()
  await execute.click()
  await page.getByText("apply-ui-1", { exact: true }).waitFor()
  assert.equal(confirms, 1)
  assert.equal(applies, 1)
  assert.deepEqual(errors, [])
  const result = { fixture: "incus-approval-component", result: "passed", browser_channel: channel ?? "headless-shell", viewports: ["1280x900", "390x844"],
    explicit_approval_required: true, tokens_in_ui: false, native_host_executed: false }
  await writeFile(path.join(reports, "result.json"), JSON.stringify(result, null, 2) + "\n", { mode: 0o600 })
  console.log(JSON.stringify(result))
} finally {
  if (browser) await browser.close()
  if (server) await server.close()
  await rm(temp, { recursive: true, force: true })
}
