"use strict";

const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const test = require("node:test");
const { patchSource, patchDirectory, rules } = require("./guard-credential-races.cjs");

test("fixed write-path drift is rejected before any file is changed", () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "anas-credential-patch-"));
  const services = path.join(root, "services");
  fs.mkdirSync(services);
  fs.mkdirSync(path.join(root, "repositories"));
  const originals = new Map();
  try {
    for (const [name, replacements] of Object.entries(rules)) {
      const file = path.join(services, name);
      const content = replacements.map(([needle]) => needle).join("\n");
      originals.set(file, content);
      fs.writeFileSync(file, content);
    }
    const last = [...originals.keys()].at(-1);
    fs.appendFileSync(last, "\n" + rules["../repositories/shared-link.repository.js"][0][0]);
    originals.set(last, fs.readFileSync(last, "utf8"));
    assert.throws(() => patchDirectory(services), /missing or repeated/);
    for (const [file, content] of originals) assert.equal(fs.readFileSync(file, "utf8"), content);
  } finally {
    fs.rmSync(root, { recursive: true });
  }
});

test("unknown, already patched and missing targets fail closed", () => {
  assert.throws(() => patchSource("arbitrary.js", ""), /unknown patch target/);
  const original = rules["../repositories/session.repository.js"][0][0];
  assert.throws(() => patchSource("../repositories/session.repository.js", ""), /missing or repeated/);
  const patched = patchSource("../repositories/session.repository.js", original);
  assert.throws(() => patchSource("../repositories/session.repository.js", patched), /already contains/);
});
