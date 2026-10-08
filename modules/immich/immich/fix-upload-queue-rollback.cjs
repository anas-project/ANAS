"use strict";
const fs = require("node:fs");
const path = require("node:path");
const marker = "// ANAS Immich v3.2.4: roll back the failed asset before queue cleanup";
const catchStart = `        catch (error) {
            await this.jobRepository.queue({
                name: enum_1.JobName.FileDelete,
                data: { files: [file.originalPath, sidecarFile?.originalPath] },
            });
            if ((0, database_1.isAssetChecksumConstraint)(error)) {`;
const oldRollback = `            if (asset) {
                await this.assetRepository.remove({ id: asset.id });
            }
            this.logger.error(\`Error uploading file \${error}\`, error?.stack);`;

function patchSource(source) {
  if (source.includes(marker)) throw new Error("upload rollback patch already applied");
  for (const needle of [catchStart, oldRollback]) {
    const count = source.split(needle).length - 1;
    if (count !== 1) throw new Error(`expected one fixed v3.2.4 upload rollback anchor, found ${count}`);
  }
  // Native cleanup enqueues FileDelete first. If the original metadata enqueue
  // failed because Valkey cannot write, cleanup also throws, bypassing rollback
  // and leaving a checksum row that makes a later upload look like a duplicate.
  return source.replace(catchStart, `        catch (error) {
            ${marker}
            if (asset) {
                await this.assetRepository.remove({ id: asset.id });
            }
            await this.jobRepository.queue({
                name: enum_1.JobName.FileDelete,
                data: { files: [file.originalPath, sidecarFile?.originalPath] },
            });
            if ((0, database_1.isAssetChecksumConstraint)(error)) {`)
    .replace(oldRollback, "            this.logger.error(`Error uploading file ${error}`, error?.stack);");
}

if (require.main === module) {
  try {
    if (!process.argv[2]) throw new Error("usage: fix-upload-queue-rollback.cjs IMMICH_DIST_SERVICES_DIRECTORY");
    const file = path.join(process.argv[2], "asset-media.service.js");
    fs.writeFileSync(file, patchSource(fs.readFileSync(file, "utf8")));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
module.exports = { patchSource, catchStart, oldRollback };
