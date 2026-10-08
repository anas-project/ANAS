"use strict";

// Fixed Immich v3.2.4 internal write paths. The native OAuth callback has
// already verified this ID token; decoding it here only retains its provenance.
const { ForbiddenException } = require("@nestjs/common");
const { sql } = require("kysely");
const apiKeyVersion = Symbol("ANAS authenticated API key version");

function denied() {
  throw new ForbiddenException("ANAS requires a current OIDC credential");
}

function issuedAt(token, anchor) {
  if (typeof token !== "string" || typeof anchor !== "string" || !anchor.trim()) denied();
  const parts = token.split(".");
  if (parts.length !== 3 || parts.some((part) => !/^[A-Za-z0-9_-]+$/.test(part))) denied();
  let claims;
  try {
    claims = JSON.parse(Buffer.from(parts[1], "base64url").toString("utf8"));
  } catch {
    denied();
  }
  if (!claims || claims.sub !== anchor || !Number.isSafeInteger(claims.iat) || claims.iat <= 0) denied();
  return timestamp(claims.iat * 1000);
}

function timestamp(value) {
  if (value === null || value === undefined) denied();
  const result = value instanceof Date ? value : new Date(value);
  if (!Number.isFinite(result.getTime()) || result.getTime() <= 0) denied();
  return result;
}

function transaction(db, work) {
  // Shared-link insertion, asset relations and their final read use one tx.
  return db.isTransaction ? work(db) : db.transaction().setIsolationLevel("read committed").execute(work);
}

async function lockAnchor(db, anchor) {
  if (typeof anchor !== "string" || !anchor.trim()) denied();
  await sql`SELECT pg_advisory_xact_lock(hashtextextended(${anchor}, 0))`.execute(db);
}

async function checkCutoff(db, anchor, provenance) {
  const row = await db.selectFrom("anas_immich_directory_revocation").select("revokedBefore")
    .where("oauthId", "=", anchor).executeTakeFirst();
  if (row && provenance.getTime() <= timestamp(row.revokedBefore).getTime()) denied();
}

async function withUser(db, userId, work) {
  return transaction(db, async (tx) => {
    // Read only to identify the immutable anchor. All decisions are made again
    // after its advisory lock, then the user row lock, in the event's order.
    const candidate = await tx.selectFrom("user").select("oauthId").where("id", "=", userId).executeTakeFirst();
    if (!candidate) denied();
    await lockAnchor(tx, candidate.oauthId);
    const user = await tx.selectFrom("user").select(["id", "oauthId", "deletedAt"])
      .where("id", "=", userId).forUpdate().executeTakeFirst();
    if (!user || user.deletedAt !== null || user.oauthId !== candidate.oauthId || !user.oauthId.trim()) denied();
    return work(tx, user);
  });
}

async function sourceIssuedAt(db, user, auth) {
  if (!auth || auth.user?.id !== user.id || (!!auth.session === !!auth.apiKey) || auth.sharedLink) denied();
  const table = auth.session ? "session" : "api_key";
  const id = (auth.session || auth.apiKey).id;
  if (typeof id !== "string" || !id) denied();
  let query = db.selectFrom(table).select("anasOAuthIssuedAt").where("id", "=", id)
    .where("userId", "=", user.id).forShare();
  if (table === "session") {
    query = query.where((eb) => eb.or([eb("expiresAt", "is", null), eb("expiresAt", ">", sql`clock_timestamp()`)]));
  } else {
    query = query.select("key");
  }
  const row = await query.executeTakeFirst();
  if (!row) denied();
  if (table === "api_key") {
    // Rotation preserves the row ID. A request authenticated before rotation
    // cannot borrow the new key's later OIDC issuance after waiting for a lock.
    const authenticatedHash = auth.apiKey[apiKeyVersion];
    if (!Buffer.isBuffer(authenticatedHash) || !Buffer.isBuffer(row.key) || !authenticatedHash.equals(row.key)) denied();
  }
  const provenance = timestamp(row.anasOAuthIssuedAt);
  await checkCutoff(db, user.oauthId, provenance);
  return provenance;
}

function bindApiKey(apiKey, authenticatedHash) {
  if (!apiKey || !Buffer.isBuffer(authenticatedHash)) denied();
  // Authentication-only context; absent from DTO projection and JSON/logs.
  Object.defineProperty(apiKey, apiKeyVersion, { value: Buffer.from(authenticatedHash), enumerable: false });
  return apiKey;
}

function updateUserRole(db, userId, isAdmin, token, persist) {
  if (typeof isAdmin !== "boolean") denied();
  return withUser(db, userId, async (tx, user) => {
    const provenance = issuedAt(token, user.oauthId);
    await checkCutoff(tx, user.oauthId, provenance);
    return persist(tx);
  });
}

function withCredentialSource(db, userId, auth, persist) {
  return withUser(db, userId, async (tx, user) => persist(tx, await sourceIssuedAt(tx, user, auth)));
}

function createSession(db, dto) {
  return withUser(db, dto.userId, async (tx, user) => {
    let provenance;
    if (dto.parentId !== undefined && dto.parentId !== null) {
      // A delegated session inherits the actual parent's verified issuance,
      // including delegates of delegates; its creation time cannot refresh it.
      if (dto.oauthBearerToken) denied();
      provenance = await sourceIssuedAt(tx, user, { user: { id: user.id }, session: { id: dto.parentId } });
    } else {
      provenance = issuedAt(dto.oauthBearerToken, user.oauthId);
      await checkCutoff(tx, user.oauthId, provenance);
    }
    return tx.insertInto("session").values({ ...dto, anasOAuthIssuedAt: provenance }).returningAll().executeTakeFirstOrThrow();
  });
}

function createUser(db, dto, token, persist) {
  const provenance = issuedAt(token, dto.oauthId);
  return transaction(db, async (tx) => {
    // This lock also serializes a first login with an event for an anchor that
    // has no Immich user yet. The event's durable marker survives that absence.
    await lockAnchor(tx, dto.oauthId);
    await checkCutoff(tx, dto.oauthId, provenance);
    return persist(tx);
  });
}

module.exports = { issuedAt, createUser, createSession, withCredentialSource, bindApiKey, updateUserRole };
