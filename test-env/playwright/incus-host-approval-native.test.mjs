import assert from "node:assert/strict"
import { test } from "node:test"
import { validateInput, validateRefresh, BrowserGateFailure, openCertificateEvidence } from "./incus-host-approval-native.mjs"

const valid = () => ({
  schema: "anas.incus-host-browser/v1", vm_id: "anas-incus-host-abcdef",
  server_spki_sha256: Buffer.alloc(32, 7).toString("base64"), main_asset_sha256: "a".repeat(64),
  session: { schema: "anas.console-session/v1", origin: "https://anas.native.test:18445",
    ca_pem: "-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----\n",
    session: { source: "local", session_token: "test-session-not-production", csrf_token: "test-csrf-not-production" } },
})

test("native browser accepts only the exact isolated origin and VM", () => {
  validateInput(valid(), "anas-incus-host-abcdef")
  for (const edit of [
    input => { input.vm_id = "anas-incus-host-123456" },
    input => { input.session.origin = "https://real-server.example" },
    input => { input.session.origin = "https://anas.native.test:18445/" },
    input => { input.session.session.source = "oidc_proxy" },
    input => { input.session.session.session_token = "" },
    input => { input.server_spki_sha256 = Buffer.alloc(31).toString("base64") },
    input => { input.main_asset_sha256 = "invalid" },
    input => { input.private_key = "not-accepted" },
  ]) {
    const input = valid(); edit(input)
    assert.throws(() => validateInput(input, "anas-incus-host-abcdef"), BrowserGateFailure)
  }
})

test("renewal requires actual elapsed plan lifetime and new unapproved display", () => {
  const evidence = { before: "job_" + "a".repeat(32), after: "job_" + "b".repeat(32),
    oldCreatedAt: "2026-09-23T00:00:00Z", newCreatedAt: "2026-09-23T00:05:01Z",
    elapsedMs: 301_000, checkboxChecked: false, applyDisabled: true, confirmations: 0, applies: 0 }
  validateRefresh(evidence)
  for (const patch of [{ after: evidence.before }, { checkboxChecked: true }, { applyDisabled: false },
    { confirmations: 1 }, { applies: 1 }, { elapsedMs: 1000 },
    { newCreatedAt: "2026-09-23T00:00:01Z" }]) {
    assert.throws(() => validateRefresh({ ...evidence, ...patch }), BrowserGateFailure)
  }
})

test("browser failures never retain or print private request data", () => {
  const error = new BrowserGateFailure("invalid-code-secret-session")
  assert.equal(error.message, "browser_gate_failed")
  assert.ok(!JSON.stringify(error).includes("secret-session"))
})

test("certificate observation starts before navigation, not after the TLS exchange", async () => {
  const calls = []
  const session = { send: async method => { calls.push(method) } }
  const context = { newCDPSession: async page => { assert.equal(page, "test-page"); return session } }
  const observer = await openCertificateEvidence(context, "test-page")
  assert.equal(observer, session)
  assert.deepEqual(calls, ["Network.enable"])
})
