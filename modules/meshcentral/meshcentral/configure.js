#!/usr/bin/env node

"use strict";

const fs = require("fs");

function required(name) {
  const value = process.env[name];
  if (value === undefined || value === "") {
    throw new Error(`missing required environment variable: ${name}`);
  }
  return value;
}

function port(name) {
  const value = required(name);
  if (!/^\d+$/.test(value)) {
    throw new Error(`${name} must be an integer`);
  }
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number < 1 || number > 65535) {
    throw new Error(`${name} must be between 1 and 65535`);
  }
  return number;
}

const source = process.argv[2];
const destination = process.argv[3];
if (!source || !destination) {
  throw new Error("usage: configure.js SOURCE DESTINATION");
}

const config = JSON.parse(fs.readFileSync(source, "utf8"));
const settings = config.settings;
const domain = config.domains[""];
const ldap = domain.ldapOptions;

settings.cert = required("MESHCENTRAL_DOMAIN");
settings.port = port("TRAEFIK_BASE_PORT");
settings.AliasPort = required("TRAEFIK_BASE_PORT");
settings.MpsPort = required("MESHCENTRAL_MPS_PORT");
settings.tlsOffload = required("TRAEFIK_IP");
delete settings.mySQL;
delete settings.mariaDB;
delete settings.postgres;
const database = {
  host: required("MESHCENTRAL_DB_HOST"),
  port: port("MESHCENTRAL_DB_PORT"),
  user: required("MESHCENTRAL_DB_USERNAME"),
  password: required("MESHCENTRAL_DB_PASSWORD"),
  database: required("MESHCENTRAL_DB_NAME"),
};
switch (required("MESHCENTRAL_DB_TYPE")) {
  case "postgres":
    settings.postgres = { ...database, createdatabase: false };
    break;
  case "mariadb":
    // Keep the existing mysql2-backed path for deployments already locked to
    // MariaDB. The host is still the selected MariaDB service.
    settings.mySQL = { ...database, ssl: false };
    break;
  default:
    throw new Error("MESHCENTRAL_DB_TYPE must be postgres or mariadb");
}

domain.certUrl = required("TRAEFIK_DOMAIN_FULL");
domain.title = required("MESHCENTRAL_TITLE");
domain.title2 = required("MESHCENTRAL_SUBTITLE");
domain.showPasswordLogin = false;
domain.unknownUserRootRedirect = "/auth-oidc";
ldap.url = required("SAMBA_DC_LDAPS_SERVER_URL_PORT");
ldap.bindDN = required("SAMBA_DC_LDAP_BIND_DN");
ldap.bindCredentials = required("SAMBA_DC_LDAP_BIND_PASSWORD");
ldap.searchBase = required("SAMBA_DC_BASE_USERS_DN");
ldap.searchFilter = required("MESHCENTRAL_USER_LOGIN_FILTER");
ldap.groupSearchBase = required("SAMBA_DC_BASE_GROUPS_ROLE_DN");
ldap.groupSearchFilter = required("SAMBA_DC_GROUP_CLASS_FILTER");
domain.orphanAgentUser = required("SAMBA_DC_ADMIN_NAME");
domain.ldapUserName = required("SAMBA_DC_USER_DISPLAY_NAME");
domain.ldapUserEmail = required("SAMBA_DC_USER_EMAIL");
delete domain.ldapUserBinaryKey;
domain.ldapUserKey = required("SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE");
domain.ldapSyncWithUserGroups = {
  filter: required("SAMBA_DC_BASE_GROUPS_ROLE_DN"),
};

const appFilter = required("SAMBA_DC_APP_FILTER");
if (appFilter !== "true" && appFilter !== "false") {
  throw new Error("SAMBA_DC_APP_FILTER must be true or false");
}
if (appFilter === "true") {
  domain.ldapUserRequiredGroupMembership =
    `memberOf=CN=APP_meshcentral,${required("SAMBA_DC_BASE_APP_DN")}`;
} else {
  delete domain.ldapUserRequiredGroupMembership;
}
domain.ldapSiteAdminGroups = required("SAMBA_DC_ADMIN_GROUP_DN");

const oidc = {
  issuer: {
    issuer: required("MESHCENTRAL_OIDC_ISSUER_URL"),
  },
  client: {
    client_id: required("MESHCENTRAL_OIDC_CLIENT_ID"),
    client_secret: required("MESHCENTRAL_OIDC_CLIENT_SECRET"),
    redirect_uri: required("MESHCENTRAL_DOMAIN_FULL") + "/auth-oidc-callback",
    post_logout_redirect_uri: required("MESHCENTRAL_DOMAIN_FULL") + "/login",
  },
  custom: {
    scope: required("MESHCENTRAL_OIDC_SCOPES"),
    claims: {
      email: "email",
      name: "name",
      uuid: required("SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"),
    },
  },
  newAccounts: true,
  groups: {
    // MeshCentral otherwise requests a separate literal "groups" scope. The
    // generic IAM profile mapping already carries the groups claim, so reuse
    // that standard scope rather than require a provider-specific extra one.
    scope: "profile",
    siteadmin: [required("SAMBA_DC_ADMIN_GROUP_NAME")],
    revokeAdmin: true,
    sync: true,
    claim: "groups",
  },
};
if (appFilter === "true") {
  oidc.groups.required = [
    "APP_meshcentral",
    "APP_all",
    required("SAMBA_DC_ADMIN_GROUP_NAME"),
  ];
}
domain.authStrategies = { ...(domain.authStrategies || {}), oidc };

const temporary = `${destination}.tmp`;
fs.writeFileSync(temporary, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
fs.renameSync(temporary, destination);
