"use strict";

const fs = require("node:fs");
const path = require("node:path");
const marker = "// ANAS Immich v3.2.4: immutable OIDC identity";
const denied = `throw new (require('@nestjs/common').ForbiddenException)('ANAS requires immutable OIDC accounts');`;
const revocationEvent = "https://schemas.openid.net/secevent/caep/event-type/session-revoked";
const identityConstraintSQL = `DO $anas$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'user_oauthId_uq' AND conrelid = 'public."user"'::regclass) THEN
    ALTER TABLE public."user" ADD CONSTRAINT "user_oauthId_uq" UNIQUE ("oauthId");
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'user_oauthId_uq' AND conrelid = 'public."user"'::regclass AND contype = 'u' AND conkey = ARRAY[(SELECT attnum FROM pg_attribute WHERE attrelid = 'public."user"'::regclass AND attname = 'oauthId')]::smallint[]) THEN
    RAISE EXCEPTION 'ANAS Immich oauthId constraint does not match the fixed definition';
  END IF;
END
$anas$;
CREATE TABLE IF NOT EXISTS public.anas_immich_logout_token (
  issuer text NOT NULL,
  "clientId" text NOT NULL,
  jti text NOT NULL,
  "expiresAt" timestamptz NOT NULL,
  PRIMARY KEY (issuer, "clientId", jti)
);
CREATE TABLE IF NOT EXISTS public.anas_immich_directory_revocation (
  "oauthId" text PRIMARY KEY,
  "revokedBefore" timestamptz NOT NULL
);
ALTER TABLE public.session ADD COLUMN IF NOT EXISTS "anasOAuthIssuedAt" timestamptz;
ALTER TABLE public.api_key ADD COLUMN IF NOT EXISTS "anasOAuthIssuedAt" timestamptz;
ALTER TABLE public.shared_link ADD COLUMN IF NOT EXISTS "anasOAuthIssuedAt" timestamptz;
DO $anas$
BEGIN
  IF (SELECT count(*) FROM pg_attribute WHERE attrelid IN ('public.session'::regclass, 'public.api_key'::regclass, 'public.shared_link'::regclass) AND attname = 'anasOAuthIssuedAt' AND atttypid = 'timestamptz'::regtype AND NOT attisdropped) <> 3 THEN
    RAISE EXCEPTION 'ANAS Immich directory credential columns do not match the fixed definition';
  END IF;
END
$anas$;`;
const logoutQueryStart = "    async invalidateOAuth({ oauthSid, oauthId }) {\n        let query = this.db.deleteFrom('session').returning('session.id');";
const logoutTransactionStart = `    async invalidateOAuth({ oauthSid, oauthId, logoutToken }) {
        if (!logoutToken || typeof logoutToken.jti !== 'string' || !logoutToken.jti.trim() || typeof logoutToken.issuer !== 'string' || typeof logoutToken.audience !== 'string' || !Number.isSafeInteger(logoutToken.issuedAt) || (logoutToken.expiresAt !== undefined && !Number.isFinite(logoutToken.expiresAt))) {
            throw new (require('@nestjs/common').BadRequestException)('Invalid logout token replay claims');
        }
        if (logoutToken.revokedBefore !== undefined && (!Number.isFinite(logoutToken.revokedBefore) || logoutToken.revokedBefore <= 0 || logoutToken.revokedBefore > logoutToken.issuedAt + 5 || !oauthId || oauthSid)) {
            throw new (require('@nestjs/common').BadRequestException)('Invalid directory revocation target');
        }
        return this.db.transaction().execute(async (db) => {
        const sql = require('kysely').sql;
        await sql\`DELETE FROM public.anas_immich_logout_token WHERE "expiresAt" < now()\`.execute(db);
        const consumed = await sql\`INSERT INTO public.anas_immich_logout_token (issuer, "clientId", jti, "expiresAt") VALUES (\${logoutToken.issuer}, \${logoutToken.audience}, \${logoutToken.jti}, now() + interval '5 minutes') ON CONFLICT DO NOTHING RETURNING jti\`.execute(db);
        if (consumed.rows.length !== 1) {
            throw new (require('@nestjs/common').BadRequestException)('Logout token already consumed');
        }
        if (!oauthSid && !oauthId) throw new Error('Invalid OAuth logout target');
        if (logoutToken.revokedBefore !== undefined) {
            // A subject may not have reached its first callback yet. The same
            // anchor lock and durable cutoff are used by native user creation.
            await sql\`SELECT pg_advisory_xact_lock(hashtextextended(\${oauthId}, 0))\`.execute(db);
        }
        // Credential creation locks the same user rows. Acquire them in stable
        // order before the receipt-time check, including ordinary session logout.
        await sql\`WITH targets AS (
            SELECT u.id FROM public."user" u WHERE
            (\${oauthId ?? null}::text IS NOT NULL AND u."oauthId" = \${oauthId ?? null}::text)
            OR (\${oauthId ?? null}::text IS NULL AND EXISTS (SELECT 1 FROM public.session s WHERE s."userId" = u.id AND s."oauthSid" = \${oauthSid ?? null}::text))
            ORDER BY u.id FOR UPDATE
        ) SELECT count(*) FROM targets\`.execute(db);
        // JOSE uses integer seconds: age=125 is valid with its 5-second
        // tolerance. Recheck on the DB connection after any queue/unique-key
        // wait, so an already-verified request cannot outlive replay retention.
        // JOSE compares exp <= floor(epoch(now)) - tolerance. Preserve its
        // NumericDate fractions and recheck a shorter optional expiry too.
        const receipt = await sql\`SELECT clock_timestamp() >= to_timestamp(\${logoutToken.issuedAt} - 5) AND clock_timestamp() < to_timestamp(\${logoutToken.issuedAt} + 126) AND (\${logoutToken.expiresAt ?? null}::double precision IS NULL OR \${logoutToken.expiresAt ?? null}::double precision > floor(EXTRACT(EPOCH FROM clock_timestamp())) - 5) AS valid\`.execute(db);
        if (receipt.rows[0]?.valid !== true) {
            throw new (require('@nestjs/common').BadRequestException)('Logout token expired before session invalidation');
        }
        // A policy event revokes credentials derived from tokens issued before
        // its detection time, including keys created while delivery was pending.
        // Persisting the cutoff also rejects an old delayed OAuth callback.
        const revokedApiKeyIds = [];
        if (logoutToken.revokedBefore !== undefined) {
            await sql\`INSERT INTO public.anas_immich_directory_revocation ("oauthId", "revokedBefore") VALUES (\${oauthId}, to_timestamp(\${logoutToken.revokedBefore})) ON CONFLICT ("oauthId") DO UPDATE SET "revokedBefore" = greatest(anas_immich_directory_revocation."revokedBefore", EXCLUDED."revokedBefore")\`.execute(db);
            const cutoff = sql\`to_timestamp(\${logoutToken.revokedBefore})\`;
            for (const table of ['api_key', 'shared_link']) {
                const revoked = await db.deleteFrom(table).using('user').whereRef('user.id', '=', table + '.userId')
                    .where('user.oauthId', '=', oauthId).where(sql\`coalesce(\${sql.ref(table + '.anasOAuthIssuedAt')}, '-infinity'::timestamptz)\`, '<=', cutoff).returning(table + '.id').execute();
                if (table === 'api_key') revokedApiKeyIds.push(...revoked.map((row) => row.id));
            }
        }
        let query = db.deleteFrom('session').returning('session.id');
        if (logoutToken.revokedBefore !== undefined) {
            query = query.where(sql\`coalesce("session"."anasOAuthIssuedAt", '-infinity'::timestamptz)\`, '<=', sql\`to_timestamp(\${logoutToken.revokedBefore})\`);
        }`;
const rules = {
  "../repositories/database.repository.js": [
    ["        this.logger.log('Finished running migrations');", "await require('kysely').sql.raw(" + JSON.stringify(identityConstraintSQL) + ").execute(this.db);", "before"],
  ],
  "../schema/tables/user.table.js": [
    ["    (0, sql_tools_1.Column)({ default: '' }),\n    __metadata(\"design:type\", Object)\n], UserTable.prototype, \"oauthId\", void 0);", "    (0, sql_tools_1.Column)({ default: '', unique: true }),\n    __metadata(\"design:type\", Object)\n], UserTable.prototype, \"oauthId\", void 0);", "replace"],
  ],
  "../repositories/oauth.repository.js": [
    ["            if (!profile.sub) {", `            if (!tokens.id_token || !tokenClaims || !Number.isSafeInteger(tokenClaims.iat) || typeof tokenClaims.sub !== 'string' || !tokenClaims.sub.trim() || profile.sub !== tokenClaims.sub) {
                throw new Error('ANAS requires a verified ID token with a matching subject and issued-at');
            }`, "before"],
    ["            return {\n                sub: payload.sub,\n                sid: payload.sid,\n            };", `            if (Object.hasOwn(payload, 'nonce')) {
                throw new Error('Logout token must not contain a nonce');
            }
            const logoutEvent = events?.['http://schemas.openid.net/event/backchannel-logout'];
            if (!events || typeof events !== 'object' || Array.isArray(events) || !logoutEvent || typeof logoutEvent !== 'object' || Array.isArray(logoutEvent) || Object.keys(logoutEvent).length !== 0) {
                throw new Error('Logout token requires a backchannel-logout event object');
            }
            if ((payload.sub !== undefined && (typeof payload.sub !== 'string' || !payload.sub.trim())) || (payload.sid !== undefined && (typeof payload.sid !== 'string' || !payload.sid.trim())) || (!payload.sub && !payload.sid)) {
                throw new Error('Logout token requires a valid subject or session identifier');
            }
            if (typeof payload.jti !== 'string' || !payload.jti.trim() || !Number.isSafeInteger(payload.iat)) {
                throw new Error('Logout token requires jti and issued-at claims');
            }
            let revokedBefore;
            if (Object.hasOwn(events, ${JSON.stringify(revocationEvent)})) {
                const event = events[${JSON.stringify(revocationEvent)}];
                const subject = payload.sub_id;
                if (!event || typeof event !== 'object' || Array.isArray(event) || event.initiating_entity !== 'policy' || !Number.isFinite(event.event_timestamp) || event.event_timestamp <= 0 || event.event_timestamp > payload.iat + 5 || typeof payload.sub !== 'string' || !payload.sub.trim() || Object.hasOwn(payload, 'sid') || !subject || subject.format !== 'iss_sub' || subject.iss !== payload.iss || subject.sub !== payload.sub) {
                    throw new Error('Invalid directory session-revoked event');
                }
                revokedBefore = event.event_timestamp;
            }
            return {
                sub: payload.sub,
                sid: payload.sid,
                jti: payload.jti,
                issuedAt: payload.iat,
                expiresAt: payload.exp,
                issuer: payload.iss,
                revokedBefore,
            };`, "replace"],
    ["            this.logger.error(`Error validating JWT logout token: ${error.message}`);\n            this.logger.error(error);\n            throw new Error('Error validating JWT logout token', { cause: error });", "            this.logger.warn('OIDC logout token rejected');\n            throw new Error('Error validating JWT logout token');", "replace"],
  ],
  "../repositories/session.repository.js": [
    [logoutQueryStart, logoutTransactionStart, "replace"],
    ["        const deletedRows = await query.execute();\n        return deletedRows.map((row) => row.id);\n    }\n    async lockAll(userId)", `        // DELETE RETURNING omits ON DELETE CASCADE children. Collect the
        // complete delegation tree first so their live sockets are closed too.
        const descendants = await sql\`WITH RECURSIVE doomed AS (
            SELECT s.id FROM public.session s JOIN public."user" u ON u.id = s."userId"
            WHERE (\${oauthId ?? null}::text IS NULL OR u."oauthId" = \${oauthId ?? null}::text)
              AND (\${oauthSid ?? null}::text IS NULL OR s."oauthSid" = \${oauthSid ?? null}::text)
              AND (\${logoutToken.revokedBefore ?? null}::double precision IS NULL OR coalesce(s."anasOAuthIssuedAt", '-infinity'::timestamptz) <= to_timestamp(\${logoutToken.revokedBefore ?? null}::double precision))
            UNION SELECT child.id FROM public.session child JOIN doomed parent ON child."parentId" = parent.id
        ) SELECT id FROM doomed\`.execute(db);
        const deletedRows = await query.execute();
        const sessionIds = [...new Set([...deletedRows, ...descendants.rows].map((row) => row.id))];
        Object.defineProperty(sessionIds, 'revokedApiKeyIds', { value: revokedApiKeyIds });
        return sessionIds;
        });
    }
    async lockAll(userId)`, "replace"],
  ],
  "base.service.js": [
    ["    async createUser(dto) {", "if (typeof dto.oauthId !== 'string' || !dto.oauthId.trim() || dto.password != null) { " + denied + " }"],
  ],
  "auth.service.js": [
    ["        const deletedSessionIds = await this.sessionRepository.invalidateOAuth({\n            oauthSid: claims.sid,\n            oauthId: claims.sub,\n        });", "        const deletedSessionIds = await this.sessionRepository.invalidateOAuth({\n            oauthSid: claims.sid,\n            oauthId: claims.sub,\n            logoutToken: { issuer: claims.issuer, audience: oauth.clientId, jti: claims.jti, issuedAt: claims.issuedAt, expiresAt: claims.expiresAt, revokedBefore: claims.revokedBefore },\n        });", "replace"],
    ["    async changePassword(auth, dto) {", denied],
    ["    async link(auth, dto, headers) {", denied],
    ["    async unlink(auth) {", denied],
  ],
  "auth-admin.service.js": [
    ["    async unlinkAll(_auth) {", denied],
  ],
  "user-admin.service.js": [
    ["    async update(auth, id, dto) {", "if (dto.password !== undefined) { " + denied + " }"],
  ],
  "user.service.js": [
    ["    async updateMe({ user }, dto) {", "if (dto.password !== undefined) { " + denied + " }"],
  ],
};

function patchSource(filename, source) {
  if (!rules[filename]) throw new Error(`unknown patch target ${filename}`);
  if (source.includes(marker)) throw new Error(`${filename} already contains the ANAS patch`);
  for (const [needle] of rules[filename]) {
    const occurrences = source.split(needle).length - 1;
    if (occurrences !== 1) throw new Error(`${filename}: expected one ${needle.trim()}, found ${occurrences}; review fixed-version patch`);
  }
  for (const [needle, guard, mode] of rules[filename]) {
    const replacement = mode === "before" ? `${marker}\n        ${guard}\n${needle}` :
      mode === "replace" ? `${marker}\n${guard}` : `${needle}\n        ${marker}\n        ${guard}`;
    source = source.replace(needle, replacement);
  }
  return source;
}

function patchDirectory(directory) {
  // Validate every target before writing any file.
  const outputs = Object.keys(rules).map((name) => [name, patchSource(name, fs.readFileSync(path.join(directory, name), "utf8"))]);
  for (const [name, source] of outputs) fs.writeFileSync(path.join(directory, name), source);
}

if (require.main === module) {
  try {
    if (!process.argv[2]) throw new Error("usage: enforce-oidc-only.cjs IMMICH_DIST_SERVICES_DIRECTORY");
    patchDirectory(process.argv[2]);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
module.exports = { patchSource, patchDirectory, rules };
