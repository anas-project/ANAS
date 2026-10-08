"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const vm = require("node:vm");
const { spawnSync } = require("node:child_process");
const { patchSource, rules, connection, send, deletedSessions, logoutDelete, repositoryDelete, rotatedKey } = require("./fix-revoked-sockets.cjs");

const websocketName = "repositories/websocket.repository.js";
const authName = "services/auth.service.js";
const apiKeyName = "services/api-key.service.js";
const websocketSource = `class Repository { ${connection}\n${send} } Repository;`;
const authSource = `class Service {
  async finish(deletedSessionIds) { ${deletedSessions} }
  async logout(auth) { ${logoutDelete} }
} Service;`;
const Repository = vm.runInNewContext(patchSource(websocketName, websocketSource));
const Service = vm.runInNewContext(patchSource(authName, authSource));
const apiKeySource = `class Service {
  async rotate(auth, id) {
    const token = 'replacement-secret';
    const newKey = await this.apiKeyRepository.update(auth.user.id, id, { key: 'replacement-hash' });
${rotatedKey}
  }
} Service;`;
const ApiKeyService = vm.runInNewContext(patchSource(apiKeyName, apiKeySource));

function fixture(auth, duringJoin = async () => {}) {
  const trace = [];
  const rooms = new Set();
  const client = {
    id: "fixture-socket", connected: true,
    join: async (room) => { trace.push(["join", room]); rooms.add(room); await duringJoin(room, client); },
    emit: (...args) => trace.push(["clientEmit", ...args]),
    disconnect: () => { trace.push(["clientDisconnect"]); client.connected = false; rooms.clear(); },
  };
  const repository = new Repository();
  repository.logger = { log() {}, error() {} };
  repository.eventRepository = { emit: async (...args) => trace.push(["event", ...args]) };
  repository.authenticate = async () => { trace.push(["authenticate"]); return typeof auth === "function" ? auth() : auth; };
  repository.server = {
    to: (room) => ({ emit: (...args) => trace.push(["roomEmit", room, ...args]) }),
    in: (room) => ({ disconnectSockets: (close) => {
      trace.push(["roomDisconnect", room, close]);
      if (rooms.has(room)) client.disconnect();
    } }),
  };
  return { repository, client, rooms, trace };
}

test("session and API-key sockets join only their own credential rooms before revalidation", async () => {
  for (const [credential, room] of [[{ session: { id: "old-session" } }, "old-session"], [{ apiKey: { id: "old-key" } }, "anas-api-key:old-key"]]) {
    const { repository, client, trace } = fixture({ user: { id: "internal-user" }, ...credential });
    await repository.handleConnection(client);
    assert.deepEqual(trace.slice(0, 4), [["authenticate"], ["join", room], ["authenticate"], ["join", "internal-user"]]);
    assert.equal(client.connected, true);
  }
});

test("revocation between authentication and joining cannot leave a connected old credential", async () => {
  let valid = true;
  const { repository, client, trace } = fixture(() => {
    if (!valid) throw new Error("credential revoked");
    return { user: { id: "internal-user" }, apiKey: { id: "old-key" } };
  }, async () => { valid = false; });
  await repository.handleConnection(client);
  assert.equal(client.connected, false);
  assert.ok(!trace.some(([event, room]) => event === "join" && room === "internal-user"));
  assert.ok(!trace.some(([event]) => event === "event"));
});

test("concurrent room disconnection prevents the socket from later joining the user room", async () => {
  const { repository, client, trace } = fixture({ user: { id: "internal-user" }, session: { id: "old-session" } }, async (_, socket) => socket.disconnect());
  await repository.handleConnection(client);
  assert.equal(client.connected, false);
  assert.ok(!trace.some(([event, room]) => event === "join" && room === "internal-user"));
  assert.ok(!trace.some(([event]) => event === "event"));
});

test("native session-delete notification is preserved and then closes the credential transport", () => {
  const { repository, client, rooms, trace } = fixture({});
  rooms.add("old-session");
  repository.clientSend("on_session_delete", "old-session", "old-session");
  assert.deepEqual(trace, [["roomEmit", "old-session", "on_session_delete", "old-session"], ["roomDisconnect", "old-session", true], ["clientDisconnect"]]);
  assert.equal(client.connected, false);
  trace.length = 0;
  repository.clientSend("on_asset_upload", "internal-user", "asset");
  assert.deepEqual(trace, [["roomEmit", "internal-user", "on_asset_upload", "asset"]]);
});

test("backchannel uses private deleted API-key IDs while preserving native session events", async () => {
  const calls = [];
  const service = new Service();
  service.websocketRepository = { clientDisconnect: (room) => calls.push(["disconnect", room]) };
  service.eventRepository = { emit: async (_, data) => calls.push(["session", data.sessionId]) };
  const revoked = ["old-session"];
  Object.defineProperty(revoked, "revokedApiKeyIds", { value: ["old-key"], enumerable: false });
  await service.finish(revoked);
  assert.deepEqual(calls, [["disconnect", "anas-api-key:old-key"], ["session", "old-session"]]);
  assert.deepEqual([...revoked], ["old-session"]);
  assert.equal(JSON.stringify(revoked), '["old-session"]');
  calls.length = 0;
  await service.finish(["logout-session"]);
  assert.deepEqual(calls, [["session", "logout-session"]]);
});

test("ordinary logout emits each actually deleted parent/descendant session and no user-room event", async () => {
  const calls = [];
  const service = new Service();
  service.sessionRepository = {
    delete: () => assert.fail("ordinary logout must use the atomic subtree delete"),
    deleteForLogout: async (sessionId, userId) => {
      calls.push(["deleteSubtree", sessionId, userId]);
      return ["old-root", "old-child", "old-grandchild"];
    },
  };
  service.eventRepository = { emit: async (event, data) => calls.push([event, data.sessionId]) };
  await service.logout({ user: { id: "internal-user" }, session: { id: "old-root" } });
  assert.deepEqual(calls, [["deleteSubtree", "old-root", "internal-user"], ["SessionDelete", "old-root"], ["SessionDelete", "old-child"], ["SessionDelete", "old-grandchild"]]);
  calls.length = 0;
  service.sessionRepository.deleteForLogout = async () => [];
  await service.logout({ user: { id: "internal-user" }, session: { id: "already-gone" } });
  assert.deepEqual(calls, []);
});

test("a fresh credential of the same user is outside the old credential disconnect room", () => {
  const { repository, client, rooms, trace } = fixture({});
  rooms.add("internal-user");
  rooms.add("anas-api-key:fresh-key");
  repository.clientDisconnect("anas-api-key:old-key");
  repository.clientSend("on_session_delete", "old-session", "old-session");
  assert.equal(client.connected, true);
  assert.ok(!trace.some(([event, room]) => event === "roomDisconnect" && room === "internal-user"));
});

test("same-ID rotation closes the canonical credential room only after a successful update", async () => {
  const calls = [];
  const service = new ApiKeyService();
  service.apiKeyRepository = { update: async (userId, id, changes) => {
    calls.push(["update", userId, id, changes.key]);
    return { id: "canonical-database-id", name: "rotated" };
  } };
  service.map = (key) => { calls.push(["map", key.id]); return key; };
  service.websocketRepository = { clientDisconnect: (room) => calls.push(["disconnect", room]) };
  const response = await service.rotate({ user: { id: "internal-user" } }, "RAW-URL-ID");
  assert.deepEqual(calls, [["update", "internal-user", "RAW-URL-ID", "replacement-hash"], ["disconnect", "anas-api-key:canonical-database-id"], ["map", "canonical-database-id"]]);
  assert.equal(response.secret, "replacement-secret");
  assert.equal(response.apiKey.id, "canonical-database-id");

  calls.length = 0;
  const failure = new Error("transaction rejected rotation");
  service.apiKeyRepository.update = async () => { throw failure; };
  await assert.rejects(service.rotate({ user: { id: "internal-user" } }, "RAW-URL-ID"), (error) => error === failure);
  assert.deepEqual(calls, []);
});

test("unknown, missing, duplicated and already patched version anchors fail closed", () => {
  for (const [name, source] of [[websocketName, websocketSource], [authName, authSource], [apiKeyName, apiKeySource], ["repositories/session.repository.js", repositoryDelete]]) {
    assert.throws(() => patchSource(name, ""), /expected one fixed v3.2.4/);
    assert.throws(() => patchSource(name, source + source), /expected one fixed v3.2.4/);
    assert.throws(() => patchSource(name, patchSource(name, source)), /already contains/);
  }
  assert.throws(() => patchSource("unsupported.js", ""), /unknown revoked socket patch target/);
});

test("CLI validates all source files before writing any patch", () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "anas-revoked-socket-patch-"));
  try {
    for (const name of Object.keys(rules)) fs.mkdirSync(path.dirname(path.join(directory, name)), { recursive: true });
    const file = path.join(directory, websocketName);
    fs.writeFileSync(file, websocketSource);
    fs.writeFileSync(path.join(directory, authName), "unknown version");
    const result = spawnSync(process.execPath, [path.join(__dirname, "fix-revoked-sockets.cjs"), directory], { encoding: "utf8" });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /expected one fixed v3.2.4/);
    assert.equal(fs.readFileSync(file, "utf8"), websocketSource);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});
