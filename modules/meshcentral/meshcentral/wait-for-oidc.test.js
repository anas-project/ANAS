"use strict";

const assert = require("node:assert/strict");
const test = require("node:test");

const { waitForOIDC } = require("./wait-for-oidc.js");

const quietLogger = { log() {}, warn() {} };

test("waits through transient discovery failures", async () => {
  let requests = 0;
  const issuer = "https://auth.example.test";
  const metadata = await waitForOIDC({
    discoveryURL: `${issuer}/.well-known/openid-configuration`,
    expectedIssuer: `${issuer}/`,
    maxAttempts: 3,
    retryMs: 1,
    requestTimeoutMs: 1000,
    logger: quietLogger,
    fetchImpl: async () => {
      requests += 1;
      if (requests < 3) return new Response("not ready", { status: 404 });
      return Response.json({
        issuer,
        authorization_endpoint: `${issuer}/oauth2/authorize`,
        token_endpoint: `${issuer}/oauth2/token`,
        jwks_uri: `${issuer}/oauth2/jwks`,
      });
    },
  });
  assert.equal(metadata.issuer, issuer);
  assert.equal(requests, 3);
});

test("rejects discovery metadata from a different issuer", async () => {
  const issuer = "https://auth.example.test";
  await assert.rejects(
    waitForOIDC({
      discoveryURL: `${issuer}/.well-known/openid-configuration`,
      expectedIssuer: issuer,
      maxAttempts: 1,
      retryMs: 1,
      requestTimeoutMs: 1000,
      logger: quietLogger,
      fetchImpl: async () =>
        Response.json({
          issuer: "https://unexpected.example.test",
          authorization_endpoint: "https://unexpected.example.test/authorize",
          token_endpoint: "https://unexpected.example.test/token",
          jwks_uri: "https://unexpected.example.test/jwks",
        }),
    }),
    /does not match/,
  );
});
