"use strict";
const fs = require("node:fs");
const path = require("node:path");

const marker = "// ANAS Immich v3.2.4: disconnect revoked credential sockets";
const connection = `    async handleConnection(client) {
        try {
            this.logger.log(\`Websocket Connect:    \${client.id}\`);
            const auth = await this.authenticate(client);
            await client.join(auth.user.id);
            if (auth.session) {
                await client.join(auth.session.id);
            }
            await this.eventRepository.emit('WebsocketConnect', { userId: auth.user.id });
        }
        catch (error) {
            this.logger.error(\`Websocket connection error: \${error}\`, error?.stack);
            client.emit('error', 'unauthorized');
            client.disconnect();
        }
    }`;
const send = `    clientSend(event, room, ...data) {
        this.server?.to(room).emit(event, ...data);
    }`;
const deletedSessions = `        for (const sessionId of deletedSessionIds) {
            await this.eventRepository.emit('SessionDelete', { sessionId });
        }`;
const logoutDelete = `            await this.sessionRepository.delete(auth.session.id);
            await this.eventRepository.emit('SessionDelete', { sessionId: auth.session.id });`;
const repositoryDelete = `    async delete(id) {
        await this.db.deleteFrom('session').where('id', '=', (0, database_2.asUuid)(id)).execute();
    }`;
const rotatedKey = `        const apiKey = this.map(newKey);
        return { ...apiKey, secret: token, apiKey };`;

const rules = {
  "repositories/websocket.repository.js": [
    [connection, `    async handleConnection(client) {
        ${marker}
        try {
            this.logger.log(\`Websocket Connect:    \${client.id}\`);
            const auth = await this.authenticate(client);
            if (auth.session) {
                await client.join(auth.session.id);
            }
            if (auth.apiKey) {
                await client.join('anas-api-key:' + auth.apiKey.id);
            }
            // Joining the credential room before revalidation closes the
            // authenticate/join race with a concurrently committed revocation.
            await this.authenticate(client);
            if (!client.connected) {
                return;
            }
            await client.join(auth.user.id);
            await this.eventRepository.emit('WebsocketConnect', { userId: auth.user.id });
        }
        catch (error) {
            this.logger.error(\`Websocket connection error: \${error}\`, error?.stack);
            client.emit('error', 'unauthorized');
            client.disconnect();
        }
    }`],
    [send, `    clientSend(event, room, ...data) {
        this.server?.to(room).emit(event, ...data);
        if (event === 'on_session_delete') {
            this.clientDisconnect(room);
        }
    }
    clientDisconnect(room) {
        this.server?.in(room).disconnectSockets(true);
    }`],
  ],
  "services/auth.service.js": [
    [logoutDelete, `            ${marker}
            const deletedSessionIds = await this.sessionRepository.deleteForLogout(auth.session.id, auth.user.id);
            for (const sessionId of deletedSessionIds) {
                await this.eventRepository.emit('SessionDelete', { sessionId });
            }`],
    [deletedSessions, `        ${marker}
        for (const apiKeyId of deletedSessionIds.revokedApiKeyIds ?? []) {
            this.websocketRepository.clientDisconnect('anas-api-key:' + apiKeyId);
        }
${deletedSessions}`],
  ],
  "services/api-key.service.js": [[rotatedKey, `        ${marker}
        // Native rotation reuses the row ID. Close transports authenticated
        // with its previous secret before returning the replacement secret.
        this.websocketRepository.clientDisconnect('anas-api-key:' + newKey.id);
${rotatedKey}`]],
  "repositories/session.repository.js": [[repositoryDelete, `${repositoryDelete}
    async deleteForLogout(id, userId) {
        ${marker}
        const sessionId = (0, database_2.asUuid)(id);
        const ownerId = (0, database_2.asUuid)(userId);
        return this.db.transaction().setIsolationLevel('read committed').execute(async (db) => {
            const sql = require('kysely').sql;
            const candidate = await db.selectFrom('user').select('oauthId').where('id', '=', ownerId).executeTakeFirst();
            if (!candidate) return [];
            if (typeof candidate.oauthId !== 'string' || !candidate.oauthId.trim()) {
                throw new Error('Session logout requires a fixed OAuth subject');
            }
            // Match credential creation and policy revocation lock ordering.
            // A delegate committed first is included below; one waiting behind
            // logout finds its actual parent gone and cannot create a session.
            await sql\`SELECT pg_advisory_xact_lock(hashtextextended(\${candidate.oauthId}, 0))\`.execute(db);
            const owner = await db.selectFrom('user').select('oauthId').where('id', '=', ownerId).forUpdate().executeTakeFirst();
            if (!owner) return [];
            if (owner.oauthId !== candidate.oauthId) throw new Error('Session logout identity changed');
            const root = await db.selectFrom('session').select('id').where('id', '=', sessionId)
                .where('userId', '=', ownerId).forUpdate().executeTakeFirst();
            if (!root) return [];
            // The user lock serializes every fixed native delegate writer;
            // FOR UPDATE on its root also conflicts with the parent's FK lock.
            const descendants = await sql\`WITH RECURSIVE doomed AS (
                SELECT s.id FROM public.session s WHERE s.id = \${sessionId}::uuid AND s."userId" = \${ownerId}::uuid
                UNION SELECT child.id FROM public.session child JOIN doomed parent ON child."parentId" = parent.id
            ) SELECT id FROM doomed\`.execute(db);
            const ids = descendants.rows.map((row) => row.id);
            const deleted = await db.deleteFrom('session').where('id', '=', sessionId).where('userId', '=', ownerId)
                .returning('id').execute();
            if (deleted.length === 0) return [];
            // Cascade rows are absent from DELETE RETURNING. Report only IDs
            // which no longer exist after the delete has completed in this tx.
            const remaining = await db.selectFrom('session').select('id').where('id', 'in', ids).execute();
            const retained = new Set(remaining.map((row) => row.id));
            return ids.filter((id) => !retained.has(id));
        });
    }`]],
};

function patchSource(name, source) {
  if (!rules[name]) throw new Error(`unknown revoked socket patch target: ${name}`);
  if (source.includes(marker)) throw new Error(`${name} already contains revoked socket patch`);
  for (const [needle] of rules[name]) {
    const count = source.split(needle).length - 1;
    if (count !== 1) throw new Error(`expected one fixed v3.2.4 ${name} anchor, found ${count}`);
  }
  return rules[name].reduce((result, [needle, replacement]) => result.replace(needle, replacement), source);
}

if (require.main === module) {
  try {
    if (!process.argv[2]) throw new Error("usage: fix-revoked-sockets.cjs IMMICH_DIST_DIRECTORY");
    // Validate every fixed-version source before changing any file.
    const outputs = Object.keys(rules).map((name) => {
      const file = path.join(process.argv[2], name);
      return [file, patchSource(name, fs.readFileSync(file, "utf8"))];
    });
    for (const [file, source] of outputs) fs.writeFileSync(file, source);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}

module.exports = { patchSource, rules, connection, send, deletedSessions, logoutDelete, repositoryDelete, rotatedKey };
