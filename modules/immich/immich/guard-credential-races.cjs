"use strict";

const fs = require("node:fs");
const path = require("node:path");
const marker = "// ANAS Immich v3.2.4: serialized OIDC credential provenance";
const helper = "require('../oidc-credentials.cjs')";
const rules = {
  "auth.service.js": [
    ["                storageLabel: storageLabel || null,\n                isAdmin,\n            });", "                storageLabel: storageLabel || null,\n                isAdmin,\n            }, oauthBearerToken);"],
    ["            user = await this.userRepository.update(user.id, { isAdmin });", "            user = await this.userRepository.updateOAuthRole(user.id, isAdmin, oauthBearerToken);"],
    ["        const apiKey = await this.apiKeyRepository.getKey(hashed);\n        if (apiKey?.user) {", `        const apiKey = await this.apiKeyRepository.getKey(hashed);
        if (apiKey?.user) {
            ${helper}.bindApiKey(apiKey, hashed);`],
  ],
  "base.service.js": [
    ["    async createUser(dto) {", "    async createUser(dto, oauthBearerToken) {"],
    ["        const user = await this.userRepository.create({ ...payload, clusterGroupId: clusterGroup.id });", "        const user = await this.userRepository.create({ ...payload, clusterGroupId: clusterGroup.id }, oauthBearerToken);"],
  ],
  "../repositories/user.repository.js": [
    ["    async create(dto) {\n        return this.db\n            .insertInto('user')\n            .values(dto)\n            .returning(database_1.columns.userAdmin)\n            .returning(withMetadata)\n            .executeTakeFirstOrThrow();\n    }", `    async create(dto, oauthBearerToken) {
        return ${helper}.createUser(this.db, dto, oauthBearerToken, (db) => db
            .insertInto('user')
            .values(dto)
            .returning(database_1.columns.userAdmin)
            .returning(withMetadata)
            .executeTakeFirstOrThrow());
    }`],
    ["    update(id, dto) {", `    updateOAuthRole(id, isAdmin, oauthBearerToken) {
        return ${helper}.updateUserRole(this.db, id, isAdmin, oauthBearerToken, (db) => db
            .updateTable('user')
            .set({ isAdmin })
            .where('user.id', '=', (0, database_2.asUuid)(id))
            .where('user.deletedAt', 'is', null)
            .returning(database_1.columns.userAdmin)
            .returning(withMetadata)
            .executeTakeFirstOrThrow());
    }
    update(id, dto) {`],
  ],
  "../repositories/session.repository.js": [
    ["    create(dto) {\n        return this.db.insertInto('session').values(dto).returningAll().executeTakeFirstOrThrow();\n    }", `    create(dto) {
        return ${helper}.createSession(this.db, dto);
    }`],
  ],
  "api-key.service.js": [
    ["            permissions: dto.permissions,\n        });", "            permissions: dto.permissions,\n        }, auth);"],
    ["        const newKey = await this.apiKeyRepository.update(auth.user.id, id, { key: hashed });", "        const newKey = await this.apiKeyRepository.update(auth.user.id, id, { key: hashed }, auth);"],
  ],
  "../repositories/api-key.repository.js": [
    ["    create(dto) {\n        return this.db.insertInto('api_key').values(dto).returning(database_1.columns.apiKey).executeTakeFirstOrThrow();\n    }", `    create(dto, auth) {
        return ${helper}.withCredentialSource(this.db, dto.userId, auth, (db, issuedAt) => db
            .insertInto('api_key').values({ ...dto, anasOAuthIssuedAt: issuedAt })
            .returning(database_1.columns.apiKey).executeTakeFirstOrThrow());
    }`],
    ["    async update(userId, id, dto) {\n        return this.db\n            .updateTable('api_key')\n            .set(dto)\n            .where('api_key.userId', '=', userId)\n            .where('id', '=', (0, database_2.asUuid)(id))\n            .returning(database_1.columns.apiKey)\n            .executeTakeFirstOrThrow();\n    }", `    async update(userId, id, dto, auth) {
        const persist = (db, changes) => db.updateTable('api_key').set(changes)
            .where('api_key.userId', '=', userId).where('id', '=', (0, database_2.asUuid)(id))
            .returning(database_1.columns.apiKey).executeTakeFirstOrThrow();
        if (Object.hasOwn(dto, 'key')) {
            return ${helper}.withCredentialSource(this.db, userId, auth,
                (db, issuedAt) => persist(db, { ...dto, anasOAuthIssuedAt: issuedAt }));
        }
        return persist(this.db, dto);
    }`],
  ],
  "shared-link.service.js": [
    ["                showExif: dto.showMetadata ?? true,\n                slug: dto.slug || null,\n            });", "                showExif: dto.showMetadata ?? true,\n                slug: dto.slug || null,\n            }, auth);"],
  ],
  "../repositories/shared-link.repository.js": [
    ["    async create(entity) {\n        const { id } = await this.db\n            .insertInto('shared_link')\n            .values(lodash_1.default.omit(entity, 'assetIds'))\n            .returningAll()\n            .executeTakeFirstOrThrow();\n        if (entity.assetIds && entity.assetIds.length > 0) {\n            await this.db\n                .insertInto('shared_link_asset')\n                .values(entity.assetIds.map((assetId) => ({ assetId, sharedLinkId: id })))\n                .execute();\n        }\n        return this.getSharedLinks(id);\n    }", `    async create(entity, auth) {
        return ${helper}.withCredentialSource(this.db, entity.userId, auth, async (db, issuedAt) => {
            const { id } = await db.insertInto('shared_link')
                .values({ ...lodash_1.default.omit(entity, 'assetIds'), anasOAuthIssuedAt: issuedAt })
                .returningAll().executeTakeFirstOrThrow();
            if (entity.assetIds && entity.assetIds.length > 0) {
                await db.insertInto('shared_link_asset')
                    .values(entity.assetIds.map((assetId) => ({ assetId, sharedLinkId: id }))).execute();
            }
            return this.getSharedLinks(id, db);
        });
    }`],
    ["    getSharedLinks(id) {\n        return this.db", "    getSharedLinks(id, db = this.db) {\n        return db"],
  ],
};

function patchSource(filename, source) {
  if (!Object.hasOwn(rules, filename)) throw new Error(`unknown patch target ${filename}`);
  if (source.includes(marker)) throw new Error(`${filename} already contains the ANAS credential patch`);
  for (const [needle] of rules[filename]) {
    if (source.split(needle).length !== 2) throw new Error(`${filename}: fixed credential write anchor is missing or repeated`);
  }
  for (const [needle, replacement] of rules[filename]) source = source.replace(needle, marker + "\n" + replacement);
  return source;
}

function patchDirectory(directory) {
  const outputs = Object.keys(rules).map((name) => [name, patchSource(name, fs.readFileSync(path.join(directory, name), "utf8"))]);
  for (const [name, source] of outputs) fs.writeFileSync(path.join(directory, name), source);
}

if (require.main === module) {
  try {
    if (process.argv.length !== 3) throw new Error("usage: guard-credential-races.cjs IMMICH_DIST_SERVICES_DIRECTORY");
    patchDirectory(process.argv[2]);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
module.exports = { patchSource, patchDirectory, rules, marker };
