// @vitest-environment vue-host
// TEST_CASES: TEMP-T-010
// REQUIREMENTS: TEMP-R-024 TEMP-R-025 TEMP-R-038
import { beforeEach, describe, expect, it, vi } from "vitest"
import { createRenderer, createSSRApp, nextTick } from "vue"
import { renderToString } from "vue/server-renderer"
import { getWorkspaceRuntime } from "../api/lifecycle"
import type { components } from "../api/schema"
import TempSwitchNotice from "../deployment/TempSwitchNotice.vue"
import TemporaryStorageStatus from "./TemporaryStorageStatus.vue"
import WorkspaceLifecycle from "./WorkspaceLifecycle.vue"

vi.mock("../api/lifecycle", () => ({
  getWorkspaceRuntime: vi.fn(), previewModuleLifecycle: vi.fn(), executeModuleLifecycle: vi.fn(),
}))

// Vue's host renderer exercises mounted requests and user events without adding
// a browser emulator. Only the native model directive's host fields are needed.
class HostNode {
  readonly children: HostNode[] = []
  readonly props: Record<string, unknown> = {}
  parent: HostNode | null = null
  text = ""
  selected = false
  multiple = false
  value: unknown
  checked = false
  constructor(readonly tag: string) {}
  get tagName(): string { return this.tag.toUpperCase() }
  get options(): HostNode[] { return this.children.filter((node) => node.tag === "option") }
  addEventListener(): void {}
}

function detach(node: HostNode): void {
  if (node.parent) node.parent.children.splice(node.parent.children.indexOf(node), 1)
  node.parent = null
}
function insert(node: HostNode, parent: HostNode, anchor: HostNode | null = null): void {
  detach(node)
  parent.children.splice(anchor ? parent.children.indexOf(anchor) : parent.children.length, 0, node)
  node.parent = parent
}
const renderer = createRenderer<HostNode, HostNode>({
  createElement: (tag) => new HostNode(tag),
  createText: (text) => Object.assign(new HostNode("text"), { text }),
  createComment: () => new HostNode("comment"),
  setText: (node, text) => { node.text = text },
  setElementText: (node, text) => { node.text = text; node.children.splice(0) },
  insert, remove: detach,
  parentNode: (node) => node.parent,
  nextSibling: (node) => node.parent?.children[node.parent.children.indexOf(node) + 1] ?? null,
  patchProp: (node, key, _previous, value) => {
    node.props[key] = value
    if (key === "value") node.value = value
    if (key === "multiple") node.multiple = Boolean(value)
  },
  insertStaticContent: (text, parent, anchor) => {
    const node = Object.assign(new HostNode("static"), { text })
    insert(node, parent, anchor)
    return [node, node]
  },
})
function findNode(node: HostNode, predicate: (node: HostNode) => boolean): HostNode | undefined {
  if (predicate(node)) return node
  for (const child of node.children) {
    const found = findNode(child, predicate)
    if (found) return found
  }
  return undefined
}
function content(node: HostNode): string { return node.text + node.children.map(content).join(" ") }
function refreshButton(root: HostNode): HostNode {
  const found = findNode(root, (node) => node.tag === "button" && content(node) === "Refresh runtime status")
  expect(found).toBeDefined()
  return found!
}
async function flushRequests(): Promise<void> {
  await Promise.resolve()
  await nextTick()
}
function runtime(module: string): components["schemas"]["WorkspaceStatusResponse"] {
  return {
    api_version: "anas.dev/api/v1", workspace_id: "test-workspace", active_deployment: "test-deployment",
    runtime_status: "running", runtime_healthy: true, runtime_probe_error: null,
    activated_at: null, verified_at: null, previous_deployments: [],
    module_runtime: [{ module, runtime: "running", health: "healthy", containers: 1,
      temp_storage: { state: "ok", issues: [] } }],
  }
}

beforeEach(() => { vi.mocked(getWorkspaceRuntime).mockReset() })

describe("temporary storage presentation", () => {
  it("shows low capacity despite running containers and clears a recovered issue", async () => {
    const module: components["schemas"]["ModuleRuntimeStatus"] = {
      module: "office", runtime: "running", health: "unhealthy", containers: 1,
      temp_storage: { state: "low_space", issues: [{ code: "temp_low_space", name: "runtime" }] },
    }
    const failed = await renderToString(createSSRApp(TemporaryStorageStatus, { modules: [module], locale: "zh" }))
    expect(failed).toContain("可用空间不足")
    expect(failed).toContain('role="alert"')
    expect(failed).toContain("temp_low_space")
    module.temp_storage = { state: "ok", issues: [] }
    const recovered = await renderToString(createSSRApp(TemporaryStorageStatus, { modules: [module], locale: "en" }))
    expect(recovered).toContain("Available")
    expect(recovered).not.toContain("temp_low_space")
    expect(recovered).not.toContain('role="alert"')
  })

  it("escapes server text and displays the exact stop/start order and interruption", async () => {
    const output = await renderToString(createSSRApp(TempSwitchNotice, {
      locale: "en",
      plan: { required: true, stop_modules: ["office<script>", "db"], start_modules: ["db", "office<script>"], session_interruption: true },
    }))
    expect(output).toContain("sessions will be interrupted")
    expect(output).not.toContain("<script>")
    expect(output).toMatch(/Stop order.*office&lt;script&gt;.*db.*Start order.*db.*office&lt;script&gt;/s)
    const unchanged = await renderToString(createSSRApp(TempSwitchNotice, {
      locale: "en", plan: { required: false, stop_modules: [], start_modules: [], session_interruption: false },
    }))
    expect(unchanged).not.toContain("restarts the entire workspace")
  })

  it("clears a failed runtime query after the user refreshes successfully", async () => {
    vi.mocked(getWorkspaceRuntime).mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(runtime("office"))
    const root = new HostNode("root")
    const app = renderer.createApp(WorkspaceLifecycle, { workspaceIds: ["main"], csrf: "csrf", locale: "en" })
    app.mount(root)
    try {
      await flushRequests()
      expect(findNode(root, (node) => node.props.class === "error-message")).toBeDefined()
      await (refreshButton(root).props.onClick as () => Promise<void>)()
      await nextTick()
      expect(content(root)).toContain("office")
      expect(findNode(root, (node) => node.props.class === "error-message")).toBeUndefined()
    } finally { app.unmount() }
  })

  it("keeps the newest workspace query loading and ignores older responses after switching back", async () => {
    let first!: (value: components["schemas"]["WorkspaceStatusResponse"]) => void
    let second!: (error: Error) => void
    let current!: (value: components["schemas"]["WorkspaceStatusResponse"]) => void
    vi.mocked(getWorkspaceRuntime)
      .mockImplementationOnce(() => new Promise((resolve) => { first = resolve }))
      .mockImplementationOnce(() => new Promise((_resolve, reject) => { second = reject }))
      .mockImplementationOnce(() => new Promise((resolve) => { current = resolve }))
    const root = new HostNode("root")
    const app = renderer.createApp(WorkspaceLifecycle, { workspaceIds: ["a", "b"], csrf: "csrf", locale: "en" })
    app.mount(root)
    try {
      const select = findNode(root, (node) => node.tag === "select")!
      ;(select.props["onUpdate:modelValue"] as (workspace: string) => void)("b")
      await nextTick()
      ;(select.props["onUpdate:modelValue"] as (workspace: string) => void)("a")
      await nextTick()
      first(runtime("stale-office"))
      second(new Error("stale failure"))
      await flushRequests()
      expect(content(root)).not.toContain("stale-office")
      expect(findNode(root, (node) => node.props.class === "error-message")).toBeUndefined()
      expect(refreshButton(root).props.disabled).toBe(true)
      current(runtime("current-office"))
      await flushRequests()
      expect(content(root)).toContain("current-office")
      expect(refreshButton(root).props.disabled).toBe(false)
    } finally { app.unmount() }
  })
})
