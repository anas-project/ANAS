"use strict";

// Opt-in isolated real PostgreSQL tests. These instantiate the patched fixed
// upstream repositories; they do not replace native OIDC/HTTP host acceptance.
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { once } = require("node:events");
const { Kysely, PostgresDialect, sql } = require("kysely");
const { Pool } = require("pg");
const dist = process.env.ANAS_CREDENTIAL_TEST_DIST;
if (!dist) throw new Error("run through credential-races-e2e.sh in a fresh test container");
const helper = require(path.join(dist, "oidc-credentials.cjs"));
const { columns } = require(path.join(dist, "database.js"));
const { UserRepository } = require(path.join(dist, "repositories/user.repository.js"));
const { SessionRepository } = require(path.join(dist, "repositories/session.repository.js"));
const { ApiKeyRepository } = require(path.join(dist, "repositories/api-key.repository.js"));
const { SharedLinkRepository } = require(path.join(dist, "repositories/shared-link.repository.js"));
const { AuthService } = require(path.join(dist, "services/auth.service.js"));

const pool = new Pool({ max: 8, application_name: process.env.PGAPPNAME || "anas-credential-test",
  options: "-c lock_timeout=15000 -c statement_timeout=30000" });
const db = new Kysely({ dialect: new PostgresDialect({ pool }) });
const users = new UserRepository(db), sessions = new SessionRepository(db), keys = new ApiKeyRepository(db), shares = new SharedLinkRepository(db);
const checks = [];
const epoch = Math.floor(Date.now() / 1000) - 20;

function jwt(sub, iat) {
  // The helper intentionally consumes provenance verified by the callback.
  // This fixture is not an assertion that a fabricated JWT passes OAuth.
  return [Buffer.from('{"alg":"RS256"}').toString("base64url"), Buffer.from(JSON.stringify({ sub, iat })).toString("base64url"), "fixture-signature"].join(".");
}
function dto(user, iat = epoch) { return { userId: user.id, token: crypto.randomBytes(32), oauthBearerToken: jwt(user.oauthId, iat) }; }
function auth(user, session) { return { user: { id: user.id }, session: { id: session.id } }; }
async function keyAuth(user, key) {
  const row = await db.selectFrom("api_key").select("key").where("id", "=", key.id).where("userId", "=", user.id).executeTakeFirstOrThrow();
  return { user: { id: user.id }, apiKey: helper.bindApiKey({ id: key.id }, row.key) };
}
function nativeAuthService() {
  const service = Object.create(AuthService.prototype);
  service.userRepository = users;
  service.apiKeyRepository = keys;
  service.cryptoRepository = { hashSha256: (secret) => crypto.createHash("sha256").update(secret).digest() };
  service.logger = { debug() {}, warn() {}, log() {} };
  return service;
}
async function nativeCallback(user, role, iat) {
  const service = nativeAuthService();
  service.getConfig = async () => ({ oauth: { enabled: true, autoRegister: true, roleClaim: "anas_role" } });
  // Only the OAuth repository boundary is supplied; the actual compiled
  // callback and UserRepository perform all role decisions and DB writes.
  service.oauthRepository = { getProfileAndOAuthSid: async () => ({ profile: { sub: user.oauthId, email: user.email, anas_role: role }, idToken: jwt(user.oauthId, iat) }) };
  service.createLoginResponse = async (account, details, sid, token) => {
    await sessions.create({ userId: account.id, token: crypto.randomBytes(32), oauthBearerToken: token });
    return account;
  };
  return service.callback({ state: "fixture-state", codeVerifier: "fixture-verifier", url: "https://callback.test/?code=fixture" }, {}, {});
}
function mark(name) { checks.push(name); console.log("PASS: " + name); }
async function forbidden(work) { await assert.rejects(work, (error) => error.getStatus?.() === 403); }

async function setup() {
  const flags = await sql`SELECT rolsuper,rolcreatedb,rolcreaterole FROM pg_roles WHERE rolname=current_user`.execute(db);
  assert.deepEqual(flags.rows, [{ rolsuper: false, rolcreatedb: false, rolcreaterole: false }]);
  await sql.raw(`CREATE TABLE "user"(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),"oauthId" text NOT NULL UNIQUE,"deletedAt" timestamptz,"isAdmin" boolean NOT NULL DEFAULT false);
CREATE TABLE anas_immich_directory_revocation("oauthId" text PRIMARY KEY,"revokedBefore" timestamptz NOT NULL);
CREATE TABLE user_metadata("userId" uuid REFERENCES "user"(id),key text,value jsonb);
CREATE TABLE session(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),"userId" uuid NOT NULL REFERENCES "user"(id),"parentId" uuid REFERENCES session(id) ON DELETE CASCADE,token bytea,"oauthBearerToken" text,"expiresAt" timestamptz,"anasOAuthIssuedAt" timestamptz,"createdAt" timestamptz NOT NULL DEFAULT now());
CREATE TABLE api_key(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),"userId" uuid NOT NULL REFERENCES "user"(id),key bytea,name text,permissions text[] NOT NULL DEFAULT '{all}',"createdAt" timestamptz NOT NULL DEFAULT now(),"updatedAt" timestamptz NOT NULL DEFAULT now(),"anasOAuthIssuedAt" timestamptz);
CREATE TABLE asset(id uuid PRIMARY KEY);
CREATE TABLE asset_exif("assetId" uuid REFERENCES asset(id));
CREATE TABLE shared_link(id uuid PRIMARY KEY DEFAULT gen_random_uuid(),"userId" uuid NOT NULL REFERENCES "user"(id),key bytea,type text,"createdAt" timestamptz NOT NULL DEFAULT now(),"anasOAuthIssuedAt" timestamptz);
CREATE TABLE shared_link_asset("sharedLinkId" uuid NOT NULL REFERENCES shared_link(id) ON DELETE CASCADE,"assetId" uuid NOT NULL REFERENCES asset(id),PRIMARY KEY("sharedLinkId","assetId"));`).execute(db);
  // Retain the real repository's projections. These fixture-only nullable
  // columns are enough to run their SQL without claiming native app migrations.
  for (const [table, fields, existing] of [["user", columns.userAdmin, ["id", "oauthId", "deletedAt", "isAdmin"]],
    ["asset_exif", columns.exif, ["assetId"]]]) {
    for (const field of new Set(fields.map((name) => name.split(".").at(-1)))) {
      if (!existing.includes(field)) await db.schema.alterTable(table).addColumn(field, "text").execute();
    }
  }
}

async function newUser(anchor = crypto.randomUUID(), iat = epoch) {
  return users.create({ oauthId: anchor, email: anchor + "@credential.test", name: "Credential race", isAdmin: true }, jwt(anchor, iat));
}

async function child(action) {
  if (action.kind === "user") return users.create(action.dto, action.token);
  const user = action.user;
  if (action.kind === "callback") return nativeCallback(user, action.role, action.iat);
  if (action.auth?.apiKey && action.authenticatedHash) helper.bindApiKey(action.auth.apiKey, Buffer.from(action.authenticatedHash, "hex"));
  if (action.kind === "session") return sessions.create(dto(user, action.iat));
  if (action.kind === "delegate") return sessions.create({ userId: user.id, parentId: action.session.id, token: crypto.randomBytes(32) });
  if (action.kind === "key") return keys.create({ userId: user.id, key: crypto.randomBytes(32), name: "blocked key", permissions: ["all"] }, action.auth);
  if (action.kind === "share") return shares.create({ userId: user.id, key: crypto.randomBytes(50), type: "INDIVIDUAL", assetIds: [] }, action.auth);
  if (action.kind === "rotate") return keys.update(user.id, action.key.id, { key: crypto.randomBytes(32) }, action.auth);
  throw new Error("unknown child action");
}

async function blockedAcrossProcess(anchor, user, action, removeSource = false) {
  const locker = await pool.connect();
  const appName = "anas-credential-waiter-" + crypto.randomUUID();
  let processChild, result, exited;
  try {
    if (action.auth?.apiKey && !action.authenticatedHash) {
      const row = await db.selectFrom("api_key").select("key").where("id", "=", action.auth.apiKey.id).executeTakeFirstOrThrow();
      action = { ...action, authenticatedHash: row.key.toString("hex") };
    }
    await locker.query("BEGIN");
    await locker.query("SELECT pg_advisory_xact_lock(hashtextextended($1,0))", [anchor]);
    if (user) await locker.query('SELECT id FROM "user" WHERE id=$1 FOR UPDATE', [user.id]);
    await locker.query('INSERT INTO anas_immich_directory_revocation("oauthId","revokedBefore") VALUES($1,to_timestamp($2)) ON CONFLICT("oauthId") DO UPDATE SET "revokedBefore"=GREATEST(anas_immich_directory_revocation."revokedBefore",excluded."revokedBefore")', [anchor, epoch + 0.5]);
    if (removeSource) {
      await locker.query('DELETE FROM session WHERE "userId"=$1', [user.id]);
      await locker.query('DELETE FROM api_key WHERE "userId"=$1', [user.id]);
    }
    processChild = spawn(process.execPath, [__filename, "--child", JSON.stringify(action)], { env: { ...process.env, PGAPPNAME: appName }, stdio: ["ignore", "pipe", "pipe"] });
    result = "";
    processChild.stdout.on("data", (chunk) => { result += chunk; });
    exited = once(processChild, "exit");
    const deadline = Date.now() + 8000;
    let waited = false;
    while (Date.now() < deadline) {
      const status = await pool.query("SELECT wait_event_type,wait_event FROM pg_stat_activity WHERE application_name=$1", [appName]);
      if (status.rows.some((row) => row.wait_event_type === "Lock" && row.wait_event === "advisory")) { waited = true; break; }
      await new Promise((resolve) => setTimeout(resolve, 30));
    }
    assert.equal(waited, true, "child never reached the real PostgreSQL anchor lock");
    await locker.query("COMMIT");
    const [status] = await exited;
    assert.equal(status, 0, "child failed outside the expected forbidden path");
    assert.deepEqual(JSON.parse(result), { forbidden: true });
  } finally {
    await locker.query("ROLLBACK").catch(() => {});
    locker.release();
    if (processChild && processChild.exitCode === null) { processChild.kill("SIGKILL"); await exited; }
  }
}

async function main() {
  await setup();
  const absent = crypto.randomUUID();
  await blockedAcrossProcess(absent, null, { kind: "user", dto: { oauthId: absent, email: "absent@credential.test", isAdmin: true }, token: jwt(absent, epoch) });
  assert.equal((await db.selectFrom("user").select("id").where("oauthId", "=", absent).execute()).length, 0);
  const admitted = await newUser(absent, epoch + 1);
  assert.equal(admitted.oauthId, absent);
  mark("unknown-sub event persists before JIT: waiting old callback cannot create a user, next-second admission succeeds");
  const roleUser = await newUser();
  await db.insertInto("anas_immich_directory_revocation").values({ oauthId: roleUser.oauthId, revokedBefore: new Date((epoch + 0.5) * 1000) }).execute();
  const ordinary = await nativeCallback(roleUser, "user", epoch + 1);
  assert.equal(ordinary.isAdmin, false);
  await forbidden(() => nativeCallback(roleUser, "admin", epoch));
  assert.equal((await users.get(roleUser.id)).isAdmin, false, "rejected old admin callback changed the persisted role");
  await blockedAcrossProcess(roleUser.oauthId, roleUser, { kind: "callback", user: roleUser, role: "admin", iat: epoch });
  assert.equal((await users.get(roleUser.id)).isAdmin, false, "waiting rejected admin callback changed the persisted role");
  assert.equal((await nativeCallback(roleUser, "admin", epoch + 2)).isAdmin, true);
  assert.equal((await nativeCallback(roleUser, "user", epoch + 3)).isAdmin, false);
  mark("compiled native callback serializes existing-user role updates: rejected old admin token cannot promote a fresh ordinary account");
  for (const kind of ["session", "delegate", "key", "share", "rotate"]) {
    const user = await newUser();
    const session = await sessions.create(dto(user));
    const key = await keys.create({ userId: user.id, key: crypto.randomBytes(32), name: "source", permissions: ["all"] }, auth(user, session));
    const source = kind === "rotate" ? await keyAuth(user, key) : auth(user, session);
    await blockedAcrossProcess(user.oauthId, user, { kind, user, session, key, auth: source, iat: epoch }, kind !== "session");
    mark("real PG subprocess " + kind + " waits behind directory lock and rejects stale token/source after commit");
  }
  const user = await newUser();
  const session = await sessions.create(dto(user));
  const childSession = await sessions.create({ userId: user.id, parentId: session.id, token: crypto.randomBytes(32) });
  assert.equal(childSession.anasOAuthIssuedAt.getTime(), epoch * 1000);
  const key = await keys.create({ userId: user.id, key: crypto.randomBytes(32), name: "lineage", permissions: ["all"] }, auth(user, childSession));
  const sourceKeyAuth = await keyAuth(user, key);
  const descendant = await keys.create({ userId: user.id, key: crypto.randomBytes(32), name: "descendant", permissions: ["all"] }, sourceKeyAuth);
  const asset = crypto.randomUUID();
  await db.insertInto("asset").values({ id: asset }).execute();
  await db.insertInto("asset_exif").values({ assetId: asset }).execute();
  const share = await shares.create({ userId: user.id, key: crypto.randomBytes(50), type: "INDIVIDUAL", assetIds: [asset] }, sourceKeyAuth);
  assert.equal(share.assets.length, 1);
  for (const [table, id] of [["api_key", descendant.id], ["shared_link", share.id]]) {
    const row = await db.selectFrom(table).select(["anasOAuthIssuedAt", "createdAt"]).where("id", "=", id).executeTakeFirstOrThrow();
    assert.equal(row.anasOAuthIssuedAt.getTime(), epoch * 1000);
    assert.ok(row.createdAt.getTime() > (epoch + 0.5) * 1000);
  }
  await assert.rejects(() => shares.create({ userId: user.id, key: crypto.randomBytes(50), type: "INDIVIDUAL", assetIds: [crypto.randomUUID()] }, sourceKeyAuth), (error) => error.code === "23503");
  assert.equal((await db.selectFrom("shared_link").select("id").where("userId", "=", user.id).execute()).length, 1);
  mark("real native share insert/asset relation/final query are one transaction; failed asset relation rolls back the share");
  await db.insertInto("anas_immich_directory_revocation").values({ oauthId: user.oauthId, revokedBefore: new Date((epoch + 0.5) * 1000) }).execute();
  await forbidden(() => sessions.create(dto(user, epoch)));
  await forbidden(() => keys.create({ userId: user.id, key: crypto.randomBytes(32), name: "late", permissions: ["all"] }, sourceKeyAuth));
  await forbidden(() => shares.create({ userId: user.id, key: crypto.randomBytes(50), type: "INDIVIDUAL", assetIds: [] }, sourceKeyAuth));
  await forbidden(() => keys.update(user.id, key.id, { key: crypto.randomBytes(32) }, sourceKeyAuth));
  const restored = await sessions.create(dto(user, epoch + 1));
  const restoredKey = await keys.create({ userId: user.id, key: crypto.randomBytes(32), name: "readmitted", permissions: ["all"] }, auth(user, restored));
  assert.equal((await db.selectFrom("api_key").select("anasOAuthIssuedAt").where("id", "=", restoredKey.id).executeTakeFirstOrThrow()).anasOAuthIssuedAt.getTime(), (epoch + 1) * 1000);
  await keys.update(user.id, restoredKey.id, { key: crypto.randomBytes(32) }, await keyAuth(user, restoredKey));
  assert.equal((await db.selectFrom("api_key").select("anasOAuthIssuedAt").where("id", "=", restoredKey.id).executeTakeFirstOrThrow()).anasOAuthIssuedAt.getTime(), (epoch + 1) * 1000);
  mark("still-existing stale source rows cannot create/rotate descendants; next-second fresh source keeps its issuance");
  const rotatedUser = await newUser();
  const oldSession = await sessions.create(dto(rotatedUser));
  const oldSecret = "old-source-" + crypto.randomUUID(), newSecret = "fresh-source-" + crypto.randomUUID();
  const nativeService = nativeAuthService();
  const oldHash = nativeService.cryptoRepository.hashSha256(oldSecret), newHash = nativeService.cryptoRepository.hashSha256(newSecret);
  const rotatedKey = await keys.create({ userId: rotatedUser.id, key: oldHash, name: "rotating-source", permissions: ["all"] }, auth(rotatedUser, oldSession));
  const authenticatedOld = await nativeService.validateApiKey(oldSecret);
  assert.equal(JSON.stringify(authenticatedOld.apiKey).includes(oldHash.toString("hex")), false);
  assert.equal(Object.keys(authenticatedOld.apiKey).includes("key"), false);
  const freshSession = await sessions.create(dto(rotatedUser, epoch + 1));
  await keys.update(rotatedUser.id, rotatedKey.id, { key: newHash }, auth(rotatedUser, freshSession));
  const authenticatedFresh = await nativeService.validateApiKey(newSecret);
  for (const kind of ["key", "share", "rotate"]) {
    await blockedAcrossProcess(rotatedUser.oauthId, rotatedUser, { kind, user: rotatedUser, key: rotatedKey,
      auth: authenticatedOld, authenticatedHash: oldHash.toString("hex") });
  }
  await forbidden(() => keys.create({ userId: rotatedUser.id, key: crypto.randomBytes(32), name: "stale-rotated-source", permissions: ["all"] }, authenticatedOld));
  await keys.create({ userId: rotatedUser.id, key: crypto.randomBytes(32), name: "fresh-rotated-source", permissions: ["all"] }, authenticatedFresh);
  await shares.create({ userId: rotatedUser.id, key: crypto.randomBytes(50), type: "INDIVIDUAL", assetIds: [] }, authenticatedFresh);
  assert.equal((await db.selectFrom("api_key").select("key").where("id", "=", rotatedKey.id).executeTakeFirstOrThrow()).key.equals(newHash), true);
  mark("compiled native API key auth freezes a private hash: old authenticated requests cannot borrow same-ID rotation provenance after real lock waits");
  await forbidden(() => sessions.create({ ...dto(user, epoch + 1), oauthBearerToken: jwt("another-anchor", epoch + 1) }));
  await forbidden(() => sessions.create({ userId: user.id, token: crypto.randomBytes(32) }));
  await forbidden(() => sessions.create({ userId: user.id, parentId: crypto.randomUUID(), token: crypto.randomBytes(32) }));
  for (const badIat of [0, -1, "1780000000", epoch + 0.25, null]) {
    await forbidden(() => sessions.create({ ...dto(user), oauthBearerToken: jwt(user.oauthId, badIat) }));
  }
  const foreign = await newUser();
  const foreignSession = await sessions.create(dto(foreign));
  await forbidden(() => sessions.create({ userId: user.id, parentId: foreignSession.id, token: crypto.randomBytes(32) }));
  await forbidden(() => keys.create({ userId: user.id, key: crypto.randomBytes(32), name: "missing-source", permissions: ["all"] }, { user: { id: user.id }, apiKey: { id: crypto.randomUUID() } }));
  await db.updateTable("session").set({ expiresAt: new Date(Date.now() - 1000) }).where("id", "=", restored.id).execute();
  await forbidden(() => sessions.create({ userId: user.id, parentId: restored.id, token: crypto.randomBytes(32) }));
  await db.updateTable("user").set({ deletedAt: new Date() }).where("id", "=", user.id).execute();
  await forbidden(() => sessions.create(dto(user, epoch + 1)));
  mark("wrong sub, invalid iat, absent/foreign/expired source and deleted account cannot issue credentials");
  const exact = await newUser();
  await db.insertInto("anas_immich_directory_revocation").values({ oauthId: exact.oauthId, revokedBefore: new Date(epoch * 1000) }).execute();
  await forbidden(() => sessions.create(dto(exact, epoch)));
  await sessions.create(dto(exact, epoch + 1));
  mark("an ID token from the exact revoked second is refused; only a later issuance can create a session");
  console.log(JSON.stringify({ status: "passed", checks, ordinary_role: true, boundary: "real PG/compiled repository concurrency; native OIDC HTTP and full host acceptance remain separate" }));
}

(async () => {
  try {
    if (process.argv[2] === "--child") {
      try { await child(JSON.parse(process.argv[3])); throw new Error("stale credential was accepted"); }
      catch (error) { if (error.getStatus?.() !== 403) throw error; console.log(JSON.stringify({ forbidden: true })); }
    } else await main();
  } catch (error) {
    console.error("FAIL: " + error.message);
    process.exitCode = 1;
  } finally { await db.destroy(); }
})();
