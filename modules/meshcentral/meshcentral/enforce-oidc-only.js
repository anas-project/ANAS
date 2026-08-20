#!/usr/bin/env node

"use strict";

const fs = require("fs");

const authenticationMarker =
  "// ANAS: Disable every password authenticator in OIDC-only mode.";
const authenticationNeedle = [
  "    // Authenticate the user",
  "    obj.authenticate = function (name, pass, domain, fn) {",
].join("\n");
const authenticationReplacement = [
  "    // Authenticate the user",
  `    ${authenticationMarker}`,
  "    function isOIDCOnlyDomain(domain) {",
  "        return ((domain != null) && (domain.showpasswordlogin === false) && (domain.authstrategies != null) && (domain.authstrategies.oidc != null));",
  "    }",
  "    obj.authenticate = function (name, pass, domain, fn) {",
  "        if (isOIDCOnlyDomain(domain)) { fn(new Error('password authentication is disabled in OIDC-only mode')); return; }",
].join("\n");
const loginHandlerMarker =
  "// ANAS: Reject password login HTTP requests in OIDC-only mode.";
const loginHandlerNeedle = [
  "    function handleLoginRequest(req, res, direct) {",
  "        const domain = checkUserIpAddress(req, res);",
  "        if (domain == null) { return; }",
].join("\n");
const loginHandlerReplacement = [
  loginHandlerNeedle,
  `        ${loginHandlerMarker}`,
  "        if (isOIDCOnlyDomain(domain)) {",
  "            parent.debug('web', 'Rejected password login because OIDC-only mode is enabled.');",
  "            res.sendStatus(404);",
  "            return;",
  "        }",
].join("\n");

function countOccurrences(source, needle) {
  return source.split(needle).length - 1;
}

function enforceOIDCOnly(source) {
  if (source.includes(authenticationMarker) || source.includes(loginHandlerMarker)) {
    throw new Error("MeshCentral webserver.js already contains the ANAS OIDC-only patch");
  }
  const authenticationOccurrences = countOccurrences(source, authenticationNeedle);
  if (authenticationOccurrences !== 1) {
    throw new Error(
      `expected one MeshCentral authenticator, found ${authenticationOccurrences}; ` +
        "review the patch for this upstream version",
    );
  }
  const loginHandlerOccurrences = countOccurrences(source, loginHandlerNeedle);
  if (loginHandlerOccurrences !== 1) {
    throw new Error(
      `expected one MeshCentral login handler, found ${loginHandlerOccurrences}; ` +
        "review the patch for this upstream version",
    );
  }
  return source
    .replace(authenticationNeedle, authenticationReplacement)
    .replace(loginHandlerNeedle, loginHandlerReplacement);
}

function main() {
  const filename = process.argv[2];
  if (!filename) {
    throw new Error("usage: enforce-oidc-only.js WEBSERVER_JS");
  }
  const source = fs.readFileSync(filename, "utf8");
  fs.writeFileSync(filename, enforceOIDCOnly(source));
}

if (require.main === module) {
  try {
    main();
  } catch (error) {
    console.error(error.message);
    process.exit(1);
  }
}

module.exports = { enforceOIDCOnly };
