"use strict";
// Execute actual compiled v3.2.4 methods in the patched image, with transport
// and repository doubles. Real HTTP/WebSocket/PG acceptance is separate.
const assert = require("node:assert/strict");
const { WebsocketRepository } = require("/usr/src/app/server/dist/repositories/websocket.repository.js");
const { AuthService } = require("/usr/src/app/server/dist/services/auth.service.js");
const { ApiKeyService } = require("/usr/src/app/server/dist/services/api-key.service.js");
const { SessionRepository } = require("/usr/src/app/server/dist/repositories/session.repository.js");

async function main() {
  const calls = [];
  const sockets = new Map();
  let valid = true;
  const context = {
    logger: { log() {}, error() {} },
    eventRepository: { emit: async (...args) => calls.push(["event", ...args]) },
    authenticate: async () => {
      calls.push(["authenticate"]);
      if (!valid) throw new Error("credential revoked");
      return { user: { id: "user" }, apiKey: { id: "old-key" } };
    },
    server: {
      to: (room) => ({ emit: (...args) => calls.push(["notify", room, ...args]) }),
      in: (room) => ({ disconnectSockets: (close) => {
        calls.push(["disconnectRoom", room, close]);
        for (const socket of sockets.values()) if (socket.rooms.has(room)) socket.disconnect();
      } }),
    },
    clientDisconnect: WebsocketRepository.prototype.clientDisconnect,
  };
  function socket(id, rooms = []) {
    const instance = {
      id, connected: true, rooms: new Set(rooms),
      join: async (room) => { instance.rooms.add(room); calls.push(["join", room]); },
      emit: (...args) => calls.push(["socketEmit", ...args]),
      disconnect: () => { instance.connected = false; instance.rooms.clear(); calls.push(["disconnect", id]); },
    };
    sockets.set(id, instance);
    return instance;
  }
  const old = socket("old");
  const fresh = socket("fresh", ["user", "anas-api-key:fresh-key", "fresh-session"]);
  await WebsocketRepository.prototype.handleConnection.call(context, old);
  assert.deepEqual(calls.slice(0, 4), [["authenticate"], ["join", "anas-api-key:old-key"], ["authenticate"], ["join", "user"]]);

  const revoked = ["old-session"];
  Object.defineProperty(revoked, "revokedApiKeyIds", { value: ["old-key"], enumerable: false });
  const authContext = {
    getConfig: async () => ({ oauth: { enabled: true, clientId: "immich" } }),
    oauthRepository: { validateLogoutToken: async () => ({ sub: "anchor", issuer: "issuer", jti: "event", issuedAt: 2_000_000_000, revokedBefore: 2_000_000_000 }) },
    sessionRepository: { invalidateOAuth: async () => revoked },
    websocketRepository: { clientDisconnect: (room) => context.clientDisconnect(room) },
    eventRepository: context.eventRepository,
  };
  await AuthService.prototype.backchannelLogout.call(authContext, { logout_token: "signed-token-double" });
  assert.equal(old.connected, false);
  assert.equal(fresh.connected, true);
  assert.ok(calls.some(([name, event, data]) => name === "event" && event === "SessionDelete" && data.sessionId === "old-session"));
  const session = socket("session", ["user", "old-session"]);
  WebsocketRepository.prototype.clientSend.call(context, "on_session_delete", "old-session", "old-session");
  assert.equal(session.connected, false);
  assert.equal(fresh.connected, true);

  // Native rotation keeps the DB UUID while replacing its secret/provenance.
  // Its successful update must close the old transports of that exact row.
  const rotatedId = "12345678-abcd-4321-abcd-1234567890ab";
  const oldRotated = socket("before-rotation", ["user", "anas-api-key:" + rotatedId]);
  const rotationContext = {
    apiKeyRepository: {
      getById: async () => ({ id: rotatedId, permissions: [] }),
      update: async (userId, id, dto, auth) => {
        assert.equal(userId, "user");
        assert.equal(id, rotatedId.toUpperCase());
        assert.equal(dto.key, "replacement-hash");
        assert.equal(auth.session.id, "fresh-source-session");
        assert.equal(oldRotated.connected, true);
        return { id: rotatedId, permissions: [], name: "rotated" };
      },
    },
    cryptoRepository: { randomBytesAsText: () => "replacement-secret", hashSha256: () => "replacement-hash" },
    websocketRepository: { clientDisconnect: (room) => context.clientDisconnect(room) },
    map: ApiKeyService.prototype.map,
  };
  const replacement = await ApiKeyService.prototype.rotate.call(rotationContext, { user: { id: "user" }, session: { id: "fresh-source-session" } }, rotatedId.toUpperCase());
  assert.equal(replacement.secret, "replacement-secret");
  assert.equal(replacement.apiKey.id, rotatedId);
  assert.equal(oldRotated.connected, false);
  assert.equal(fresh.connected, true);
  const reconnected = socket("after-rotation");
  await WebsocketRepository.prototype.handleConnection.call({ ...context, authenticate: async () => ({ user: { id: "user" }, apiKey: { id: rotatedId } }) }, reconnected);
  assert.equal(reconnected.connected, true);
  assert.ok(reconnected.rooms.has("anas-api-key:" + rotatedId));
  const failure = new Error("transaction rejected rotation");
  const disconnectsBeforeFailure = calls.filter(([name]) => name === "disconnectRoom").length;
  rotationContext.apiKeyRepository.update = async () => { throw failure; };
  await assert.rejects(ApiKeyService.prototype.rotate.call(rotationContext, { user: { id: "user" } }, rotatedId), (error) => error === failure);
  assert.equal(calls.filter(([name]) => name === "disconnectRoom").length, disconnectsBeforeFailure);
  assert.equal(reconnected.connected, true);

  const parent = socket("logout-parent", ["user", "logout-parent-session"]);
  const child = socket("logout-child", ["user", "logout-child-session"]);
  const grandchild = socket("logout-grandchild", ["user", "logout-grandchild-session"]);
  const logoutContext = {
    sessionRepository: {
      get: async () => ({ oauthBearerToken: "verified-id-token-double" }),
      delete: () => assert.fail("logout must use the transaction which returns cascade IDs"),
      deleteForLogout: async (id, userId) => {
        assert.equal(id, "logout-parent-session");
        assert.equal(userId, "user");
        return ["logout-parent-session", "logout-child-session", "logout-grandchild-session"];
      },
    },
    eventRepository: { emit: async (name, { sessionId }) => {
      assert.equal(name, "SessionDelete");
      WebsocketRepository.prototype.clientSend.call(context, "on_session_delete", sessionId, sessionId);
    } },
    getLogoutEndpoint: async (_, token) => { assert.equal(token, "verified-id-token-double"); return "https://issuer.example/logout"; },
  };
  const logout = await AuthService.prototype.logout.call(logoutContext, { user: { id: "user" }, session: { id: "logout-parent-session" } }, "oauth");
  assert.equal(logout.successful, true);
  assert.ok([parent, child, grandchild].every((client) => !client.connected));
  assert.equal(fresh.connected, true);

  // Deterministic race: a credential is removed after the first authentication
  // but before joining completes. The second DB authentication rejects it.
  const raced = socket("raced");
  raced.join = async (room) => { raced.rooms.add(room); valid = false; };
  await WebsocketRepository.prototype.handleConnection.call(context, raced);
  assert.equal(raced.connected, false);
  assert.ok(!raced.rooms.has("user"));
  console.log("PASS: actual v3.2.4 WebsocketRepository/AuthService/ApiKeyService methods close revoked and rotated credential transports plus ordinary-logout descendants, preserve fresh sockets/new-secret reconnect, skip failed-rotation disconnect and reject authentication/join revocation race");
  if (process.env.ANAS_REVOKED_SOCKET_FIXTURE_DATABASE_URL) await verifyDatabase(process.env.ANAS_REVOKED_SOCKET_FIXTURE_DATABASE_URL);
}

async function verifyDatabase(connectionString) {
  const { randomUUID } = require("node:crypto");
  const { Kysely, PostgresDialect } = require("kysely");
  const { Pool } = require("pg");
  const pools = [];
  const databases = [];
  const held = new Set();
  function connection(name) {
    const pool = new Pool({ connectionString, application_name: "anas-revoked-socket-" + name, max: 2 });
    const db = new Kysely({ dialect: new PostgresDialect({ pool }) });
    pools.push(pool);
    databases.push(db);
    return { pool, db };
  }
  const monitor = connection("monitor");
  const delegate = connection("delegate");
  const logout = connection("logout");
  const rawInsert = connection("foreign-key");
  const repository = (db) => ({ db });
  async function waiting(name, event) {
    const deadline = Date.now() + 10_000;
    while (Date.now() < deadline) {
      const result = await monitor.pool.query("SELECT 1 FROM pg_stat_activity WHERE application_name = $1 AND wait_event_type = 'Lock' AND wait_event = $2", ["anas-revoked-socket-" + name, event]);
      if (result.rowCount) return;
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    throw new Error("expected real PostgreSQL lock wait for " + name + "/" + event);
  }
  const userId = randomUUID();
  const otherId = randomUUID();
  const anchor = "fixture-anchor-" + randomUUID();
  const issuedAt = Math.floor(Date.now() / 1000) - 10;
  const token = [Buffer.from('{"alg":"RS256"}').toString("base64url"), Buffer.from(JSON.stringify({ sub: anchor, iat: issuedAt })).toString("base64url"), "already-verified-fixture"].join(".");
  async function create(id, parentId = null) {
    return SessionRepository.prototype.create.call(repository(delegate.db), {
      id, userId, parentId, expiresAt: null, token: randomUUID(), ...(parentId ? {} : { oauthBearerToken: token }),
    });
  }
  async function barrier() {
    const client = await monitor.pool.connect();
    held.add(client);
    await client.query("BEGIN");
    await client.query("SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", [anchor]);
    return client;
  }
  try {
    // Explicit fixture-only URL: this database is created by an isolated test
    // harness, has a non-superuser owner, and contains only these test tables.
    const flags = await monitor.pool.query("SELECT rolsuper, rolcreaterole, rolcreatedb FROM pg_roles WHERE rolname=current_user");
    assert.deepEqual(flags.rows[0], { rolsuper: false, rolcreaterole: false, rolcreatedb: false });
    await monitor.pool.query(`CREATE TABLE "user" (id uuid PRIMARY KEY, "oauthId" text NOT NULL UNIQUE, "deletedAt" timestamptz);
      CREATE TABLE anas_immich_directory_revocation ("oauthId" text PRIMARY KEY, "revokedBefore" timestamptz NOT NULL);
      CREATE TABLE session (id uuid PRIMARY KEY, "userId" uuid NOT NULL REFERENCES "user"(id), "parentId" uuid REFERENCES session(id) ON DELETE CASCADE,
        "expiresAt" timestamptz, token text NOT NULL, "oauthBearerToken" text, "anasOAuthIssuedAt" timestamptz);`);
    await monitor.pool.query('INSERT INTO "user"(id,"oauthId") VALUES ($1,$2),($3,$4)', [userId, anchor, otherId, "other-anchor"]);
    const freshId = randomUUID();
    await create(freshId);
    const otherSession = randomUUID();
    await monitor.pool.query('INSERT INTO session(id,"userId",token) VALUES($1,$2,$3)', [otherSession, otherId, "other-fixture-token"]);

    // Delegate waits first on the same real advisory lock and commits before
    // ordinary logout. The delete must return its child as well as the tree.
    const parent1 = randomUUID(), child1 = randomUUID(), grandchild1 = randomUUID(), concurrent1 = randomUUID();
    await create(parent1); await create(child1, parent1); await create(grandchild1, child1);
    const first = await barrier();
    const creating = create(concurrent1, child1).then((value) => ({ value }), (error) => ({ error }));
    await waiting("delegate", "advisory");
    const deleting = SessionRepository.prototype.deleteForLogout.call(repository(logout.db), parent1, userId);
    await waiting("logout", "advisory");
    await first.query("COMMIT"); first.release(); held.delete(first);
    assert.ok((await creating).value);
    const ids = await deleting;
    assert.deepEqual(new Set(ids), new Set([parent1, child1, grandchild1, concurrent1]));

    // Ordinary logout waits first and commits before the delegated request.
    // Native credential creation must then recheck its missing parent.
    const parent2 = randomUUID(), concurrent2 = randomUUID();
    await create(parent2);
    const second = await barrier();
    const deletingFirst = SessionRepository.prototype.deleteForLogout.call(repository(logout.db), parent2, userId);
    await waiting("logout", "advisory");
    const creatingSecond = create(concurrent2, parent2).then((value) => ({ value }), (error) => ({ error }));
    await waiting("delegate", "advisory");
    await second.query("COMMIT"); second.release(); held.delete(second);
    assert.deepEqual(await deletingFirst, [parent2]);
    assert.equal((await creatingSecond).error.getStatus(), 403);

    // Independently prove the FK protection: a root FOR UPDATE blocks a raw
    // child FK check, and removing the root makes the waiting insertion fail.
    const parent3 = randomUUID(), concurrent3 = randomUUID();
    await create(parent3);
    const third = await monitor.pool.connect();
    held.add(third);
    await third.query("BEGIN");
    await third.query('SELECT id FROM session WHERE id=$1 FOR UPDATE', [parent3]);
    const foreignKeyInsert = rawInsert.pool.query('INSERT INTO session(id,"userId","parentId",token) VALUES ($1,$2,$3,$4)', [concurrent3, userId, parent3, "foreign-key-fixture-token"]).then((value) => ({ value }), (error) => ({ error }));
    await waiting("foreign-key", "transactionid");
    await third.query("DELETE FROM session WHERE id=$1", [parent3]);
    await third.query("COMMIT"); third.release(); held.delete(third);
    assert.equal((await foreignKeyInsert).error.code, "23503");

    assert.deepEqual(await SessionRepository.prototype.deleteForLogout.call(repository(logout.db), freshId, otherId), []);
    assert.deepEqual(await SessionRepository.prototype.deleteForLogout.call(repository(logout.db), parent1, userId), []);
    const survivors = await monitor.pool.query("SELECT id FROM session ORDER BY id");
    assert.deepEqual(new Set(survivors.rows.map(({ id }) => id)), new Set([freshId, otherSession]));
    console.log("PASS: real PostgreSQL ordinary-role/native methods prove delegate-before/logout-before ordering, FK protection, cascade IDs, idempotence and untouched fresh/other-user sessions");
  } finally {
    for (const client of held) {
      try { await client.query("ROLLBACK"); } finally { client.release(); }
    }
    for (const db of databases) await db.destroy();
  }
}
main().catch((error) => { console.error(error); process.exitCode = 1; });
