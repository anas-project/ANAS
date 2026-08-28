"use strict";

// TEST_CASES: MCO-T-001

const assert = require("node:assert/strict");
const test = require("node:test");

const { enforceOIDCOnly } = require("./enforce-oidc-only.js");

const upstreamWebserver = [
  "    // Authenticate the user",
  "    obj.authenticate = function (name, pass, domain, fn) {",
  "        authenticateUpstream(name, pass, domain, fn);",
  "    }",
  "    function handleLoginRequest(req, res, direct) {",
  "        const domain = checkUserIpAddress(req, res);",
  "        if (domain == null) { return; }",
  "        if (req.body == null) { res.sendStatus(404); return; }",
].join("\n");

test("rejects password authentication when OIDC-only mode is configured", () => {
  const patched = enforceOIDCOnly(upstreamWebserver);
  assert.match(patched, /function isOIDCOnlyDomain\(domain\)/);
  assert.match(patched, /password authentication is disabled in OIDC-only mode/);
  assert.match(patched, /if \(isOIDCOnlyDomain\(domain\)\)/);
  assert.match(patched, /res\.sendStatus\(404\)/);
  assert.ok(
    patched.indexOf("Rejected password login") < patched.indexOf("req.body == null"),
  );
});

test("fails closed when the pinned upstream authenticator changes", () => {
  assert.throws(
    () => enforceOIDCOnly("function handleLoginRequest() {}"),
    /expected one MeshCentral authenticator, found 0/,
  );
});

test("fails closed when the pinned upstream login handler changes", () => {
  assert.throws(
    () => enforceOIDCOnly(authenticationNeedleFixture()),
    /expected one MeshCentral login handler, found 0/,
  );
});

test("refuses to apply the patch twice", () => {
  const patched = enforceOIDCOnly(upstreamWebserver);
  assert.throws(() => enforceOIDCOnly(patched), /already contains/);
});

function authenticationNeedleFixture() {
  return [
    "    // Authenticate the user",
    "    obj.authenticate = function (name, pass, domain, fn) {",
  ].join("\n");
}
