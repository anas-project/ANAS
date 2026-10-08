// TEST_CASES: TEMP-T-021
import { test, expect, request, errors } from "@playwright/test";
import { randomBytes } from "node:crypto";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { cleanupDocument } from "./workspace-temp-storage-collabora-cleanup.mjs";
import { focusDocumentCanvas } from "./workspace-temp-storage-collabora-focus.mjs";

function required(name) {
  if (!process.env[name]) throw new Error(`${name} is required`);
  return process.env[name];
}

function officeDocument(marker) {
  const program = `import io,sys,zipfile\nb=io.BytesIO()\nwith zipfile.ZipFile(b,'w') as z:\n z.writestr('mimetype','application/vnd.oasis.opendocument.text',compress_type=zipfile.ZIP_STORED)\n z.writestr('META-INF/manifest.xml','<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"><manifest:file-entry manifest:full-path="/" manifest:media-type="application/vnd.oasis.opendocument.text"/><manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/></manifest:manifest>')\n z.writestr('content.xml','<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" office:version="1.2"><office:body><office:text><text:p>'+sys.argv[1]+'</text:p></office:text></office:body></office:document-content>')\nsys.stdout.buffer.write(b.getvalue())`;
  const result = spawnSync(process.env.ANAS_TEST_PYTHON || "python3", ["-c", program, marker]);
  if (result.status !== 0) throw new Error("ODT fixture creation failed");
  return result.stdout;
}

function documentText(body) {
  const result = spawnSync(process.env.ANAS_TEST_PYTHON || "python3", ["-c",
    "import io,sys,zipfile,xml.etree.ElementTree as E; z=zipfile.ZipFile(io.BytesIO(sys.stdin.buffer.read())); r=E.fromstring(z.read('content.xml')); print(''.join(r.itertext()))"], { input: body });
  if (result.status !== 0) throw new Error("saved document is not a valid ODT");
  return result.stdout.toString();
}

const EDITOR_INPUT = 'div.clipboard#clipboard-area[contenteditable="true"]';

async function editorFrame(page) {
  await expect.poll(() => page.frames().filter(frame => /\/browser\/[^/]+\/cool\.html/.test(frame.url())).length,
    { timeout: 180_000, message: "real Collabora editing frame must load" }).toBe(1);
  const frame = page.frames().find(item => /\/browser\/[^/]+\/cool\.html/.test(item.url()));
  await frame.locator("#document-container").waitFor({ state: "visible", timeout: 180_000 });
  await frame.locator("#document-canvas").waitFor({ state: "visible", timeout: 180_000 });
  await expect(frame.locator("#map.initialized")).toBeVisible({ timeout: 180_000 });
  await expect.poll(() => frame.evaluate(() => {
    try {
      const map = window.app?.map;
      const editable = typeof map?.isEditMode === "function" ? map.isEditMode() : null;
      return {
        loaded: typeof map?._docLoaded === "boolean" ? map._docLoaded : null,
        editable: typeof editable === "boolean" ? editable : null,
      };
    } catch { return { loaded: null, editable: null }; }
  }), { timeout: 180_000, message: "real Collabora document must finish loading in edit mode" })
    .toEqual({ loaded: true, editable: true });
  await frame.locator(EDITOR_INPUT).waitFor({ state: "attached", timeout: 180_000 });
  await expect(frame.locator("#busypopup-overlay")).toHaveCount(0, { timeout: 180_000 });
  await focusDocumentCanvas(frame, errors.TimeoutError);
  await expect.poll(() => frame.evaluate(() => {
    try {
      const map = window.app?.map;
      const focused = typeof map?.editorHasFocus === "function" ? map.editorHasFocus() : null;
      return typeof focused === "boolean" ? focused : null;
    } catch { return null; }
  }), { timeout: 180_000, message: "real Collabora document must receive Core input focus" }).toBe(true);
  return frame;
}

async function appendText(frame, text) {
  // CODE's real keyboard input node must receive focus and every key event.
  const input = frame.locator(EDITOR_INPUT);
  await input.press(process.platform === "darwin" ? "Meta+ArrowDown" : "Control+End");
  await input.press("Enter");
  await input.pressSequentially(text, { delay: 30 });
  await input.press("ControlOrMeta+s");
}

async function readClipboard(frame, expected, message) {
  const input = frame.locator(EDITOR_INPUT);
  await input.press(process.platform === "darwin" ? "Meta+ArrowUp" : "Control+Home");
  await input.press("ControlOrMeta+a");
  // CODE's upstream selectAllText waits for both real document selection handles.
  await frame.locator(".text-selection-handle-start").waitFor({ state: "attached", timeout: 30_000 });
  await frame.locator(".text-selection-handle-end").waitFor({ state: "attached", timeout: 30_000 });
  await input.press("ControlOrMeta+c");
  await expect.poll(async () => {
    try { return await frame.evaluate(() => navigator.clipboard.readText()); }
    catch { return ""; }
  }, { timeout: 30_000, message }).toContain(expected);
}

function lifecycleAction(action) {
  if (!["stop-start", "rebuild", "switch-a-to-b"].includes(action)) throw new Error("unknown fixed lifecycle action");
  const helper = fileURLToPath(new URL("../scripts/server-workspace-temp-collabora-action.py", import.meta.url));
  let executable = process.env.ANAS_TEST_PYTHON || "python3";
  let arguments_ = [helper, "--client", action];
  if (process.env.ANAS_TEST_LIFECYCLE_SSH_TARGET) {
    const target = required("ANAS_TEST_LIFECYCLE_SSH_TARGET");
    const root = required("ANAS_TEST_REMOTE_WORK_ROOT");
    const namespace = required("ANAS_TEST_REMOTE_NETNS");
    const runId = required("ANAS_TEST_RUN_ID");
    if (!/^[a-zA-Z0-9_.-]+@[a-zA-Z0-9.-]+$/.test(target)
        || !/^[a-zA-Z0-9_.-]{1,63}$/.test(runId)
        || root !== `/home/whl/anas-temp-storage-e2e/${runId}`
        || !/^anas-[a-zA-Z0-9_-]+$/.test(namespace)) throw new Error("invalid isolated SSH action endpoint");
    const remote = ["sudo", "ip", "netns", "exec", namespace, "env",
      `ANAS_TEST_WORK_ROOT=${root}`, `ANAS_TEST_RUN_ID=${runId}`, "python3",
      `${root}/src/test-env/scripts/server-workspace-temp-collabora-action.py`, "--client", action];
    const quote = value => `'${value.replaceAll("'", "'\\''")}'`;
    executable = "ssh";
    arguments_ = ["-T", "-o", "BatchMode=yes", target, remote.map(quote).join(" ")];
  }
  // The client can submit only the three actions above. A separate, private
  // UNIX-socket server executes them in the verified host test namespaces.
  const result = spawnSync(executable, arguments_,
    { timeout: 1_800_000, maxBuffer: 64 * 1024 });
  if (result.status !== 0) throw new Error(`isolated Collabora lifecycle ${action} failed (output suppressed)`);
  const evidence = JSON.parse(result.stdout.toString());
  expect(evidence.status).toBe("passed");
  expect(evidence.action).toBe(action);
  expect(evidence.fresh_container).toBe(true);
  expect(evidence.fresh_lease).toBe(true);
  expect(evidence.namespace_marker_verified).toBe(true);
  if (action === "switch-a-to-b") expect(evidence.all_containers_rebuilt).toBe(true);
}

test("Collabora saves one document across normal stop/start, rebuild, and temporary root switch", async ({ page, context }) => {
  const base = new URL(required("ANAS_TEST_NEXTCLOUD_URL"));
  const username = required("ANAS_TEST_USERNAME");
  const password = required("ANAS_TEST_PASSWORD");
  const entryIP = required("ANAS_TEST_ENTRY_IP");
  const runId = required("ANAS_TEST_RUN_ID");
  const cleanupReportFile = required("ANAS_TEST_REPORT_FILE") + ".cleanup.json";
  const id = randomBytes(8).toString("hex");
  const initial = `ANAS_TEMP_INITIAL_${id}`;
  const marker = `ANAS_TEMP_SAVED_${id}`;
  const filename = `anas-temp-${id}.odt`;
  const davPath = `/remote.php/dav/files/${encodeURIComponent(username)}/${filename}`;
  const direct = new URL(base);
  direct.hostname = entryIP;
  const dav = await request.newContext({
    baseURL: direct.origin,
    ignoreHTTPSErrors: true,
    extraHTTPHeaders: { Host: base.host, Authorization: `Basic ${Buffer.from(`${username}:${password}`).toString("base64")}` },
  });
  const call = async (method, path, options = {}) => {
    try { return await dav.fetch(path, { method, ...options }); }
    catch { throw new Error("Nextcloud WebDAV network request failed (credentials suppressed)"); }
  };
  let created = false;
  let failure;
  try {
    const upload = await call("PUT", davPath, { data: officeDocument(initial) });
    created = true;
    if (![201, 204].includes(upload.status())) throw new Error(`WebDAV upload HTTP ${upload.status()}`);
    const properties = await call("PROPFIND", davPath, { headers: { Depth: "0", "Content-Type": "application/xml" },
      data: '<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns"><d:prop><oc:fileid/></d:prop></d:propfind>' });
    if (properties.status() !== 207) throw new Error(`WebDAV properties HTTP ${properties.status()}`);
    const match = (await properties.text()).match(/<(?:[\w-]+:)?fileid>\s*(\d+)\s*<\//);
    if (!match) throw new Error("WebDAV file ID is absent");
    // Nextcloud 34 resolves the file ID through /f/{id}; openfile is a boolean.
    const fileURL = `${base.origin}/f/${match[1]}`;
    // Grant within this isolated context so the cross-origin Office iframe can
    // use the real clipboard while embedded by Nextcloud.
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await page.goto(process.env.ANAS_TEST_NEXTCLOUD_LOGIN_URL || `${base.origin}/login?direct=1`, { waitUntil: "domcontentloaded" });
    await page.locator('input[name="user"]').fill(username);
    await page.locator('input[type="password"]').fill(password);
    await page.locator('button[type="submit"], input[type="submit"]').first().click();
    await expect.poll(() => new URL(page.url()).pathname.includes("/login"), { timeout: 120_000 }).toBe(false);
    // Incidental resources may keep window.load pending after DOM readiness.
    // The actual editor, editable document, focus and saved bytes remain required.
    await page.goto(fileURL, { waitUntil: "domcontentloaded" });
    let frame = await editorFrame(page);
    await appendText(frame, marker);
    // UI readiness and success notifications alone are insufficient: read the
    // saved file from Nextcloud and inspect its real XML content.
    await expect.poll(async () => {
      const saved = await call("GET", davPath);
      if (saved.status() !== 200) throw new Error(`WebDAV saved file HTTP ${saved.status()}`);
      return documentText(await saved.body()).includes(marker);
    }, { timeout: 180_000, intervals: [2000], message: "saved ODT must contain text entered in the real editor" }).toBe(true);
    await page.goto(`${base.origin}/apps/files/`, { waitUntil: "domcontentloaded" });
    await expect.poll(() => page.frames().some(item => /\/cool\.html/.test(item.url()))).toBe(false);
    await page.goto(fileURL, { waitUntil: "domcontentloaded" });
    frame = await editorFrame(page);
    await readClipboard(frame, marker, "reopened document clipboard must include the saved edit");
    const reopened = await call("GET", davPath);
    if (reopened.status() !== 200) throw new Error(`WebDAV reopen HTTP ${reopened.status()}`);
    const content = documentText(await reopened.body());
    expect(content).toContain(initial);
    expect(content).toContain(marker);
    const expectedText = [initial, marker];
    for (const action of ["stop-start", "rebuild", "switch-a-to-b"]) {
      const edit = `ANAS_TEMP_${action.replaceAll("-", "_").toUpperCase()}_${id}`;
      await appendText(frame, edit);
      expectedText.push(edit);
      await expect.poll(async () => {
        const saved = await call("GET", davPath).catch(() => null);
        return saved?.status() === 200 && documentText(await saved.body()).includes(edit);
      }, { timeout: 180_000, intervals: [2000], message: `real edit must reach WebDAV before ${action}` }).toBe(true);
      // Keep the real editing session open while Core performs normal stop.
      lifecycleAction(action);
      await expect.poll(async () => {
        const saved = await call("GET", davPath).catch(() => null);
        if (saved?.status() !== 200) return false;
        const text = documentText(await saved.body());
        return expectedText.every(value => text.includes(value));
      }, { timeout: 300_000, intervals: [2000], message: `WebDAV content must survive ${action}` }).toBe(true);
      await page.goto(`${base.origin}/apps/files/`, { waitUntil: "domcontentloaded" });
      await page.goto(fileURL, { waitUntil: "domcontentloaded" });
      frame = await editorFrame(page);
      await readClipboard(frame, edit, `reopened editor must contain every saved edit after ${action}`);
      const saved = await call("GET", davPath);
      expect(saved.status()).toBe(200);
      const afterAction = documentText(await saved.body());
      for (const value of expectedText) expect(afterAction).toContain(value);
    }
  } catch (error) {
    failure = error;
  } finally {
    await page.goto(`${base.origin}/apps/files/`, { waitUntil: "domcontentloaded" }).catch(() => {});
    ({ failure } = await cleanupDocument({ created, remove: () => call("DELETE", davPath), dispose: () => dav.dispose(),
      firstFailure: failure, reportFile: cleanupReportFile, runId }));
  }
  if (failure) throw failure;
});
