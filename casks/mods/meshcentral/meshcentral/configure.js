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
settings.mySQL.host = required("MYSQL_HOST");
settings.mySQL.port = required("MYSQL_PORT");
settings.mySQL.user = required("MYSQL_USERNAME");
settings.mySQL.password = required("MYSQL_PASSWORD");

domain.certUrl = required("TRAEFIK_DOMAIN_FULL");
domain.title = required("MESHCENTRAL_TITLE");
domain.title2 = required("MESHCENTRAL_SUBTITLE");
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

const temporary = `${destination}.tmp`;
fs.writeFileSync(temporary, `${JSON.stringify(config, null, 2)}\n`, { mode: 0o600 });
fs.renameSync(temporary, destination);
