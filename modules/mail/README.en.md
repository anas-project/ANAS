# ANAS Mail

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `mail` |
| Version / revision | `0.1.0-r1` |
| Status | `developing` |
| Category | `app` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

Current candidates include Go mail management, the fixed DMS control process and the Roundcube plugin. Manifest/Compose wiring declares generic OIDC, read-only Samba access, independent database resources and private managed runtime bindings. **The finance candidate is deployed through HTTPS port 10000, with a real account entering Roundcube without a second login. Phase-one acceptance remains incomplete; cloud ingress and production outbound delivery are not enabled.** The [root plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/mail-integration.md) records scope, baselines and blockers; prototypes and isolated subsets do not establish complete acceptance.

## Dependencies and enablement prerequisites

Reuse existing PostgreSQL, IAM OIDC, Samba AD, Traefik and the user portal. No new database or authentication service is introduced. Go uses primary_database and Roundcube uses webmail, with separate databases and non-superuser credentials, both retained on removal. Go uses the AD reader for identity lookup and a separate restricted writer for address management. The portal checks address conflicts during account creation.

Before enablement, ANAS must verify the complete stopped backup and stop external directory writers, then explicitly prepare Samba mail resources and roles. samba_dc.mail_management_policy must specify managed domains, protected addresses, mailbox and alias counts, capacity and sending rate. mail_initial_source_domain identifies the converged old domain and mail_initial_snapshot_id identifies the verified initial snapshot. Completion pins the original baseline and role SIDs; normal restarts never reimport it or adopt replacement accounts. Unmapped legacy SMTP values stop import rather than infer mailbox ownership.

```yaml
modules:
  mail:
    enabled: true
    config:
      domain_prefix: mail
      webmail_domain_prefix: webmail
      iam_protocol: oidc
      db_type: postgres
```

This is a candidate configuration awaiting acceptance. Saving it does not make mail ready. Cloud ingress, Resend/compatible SMTP, Tutorial and consistent recovery still require the root plan’s actual checks before business enablement.

## Personnel, mailboxes and administration

Personnel log in with the IAM-verified anasIdentityAnchor and fresh AD application/all/administrator-group admission. Role anchors are pinned once; renamed groups retain identity and replacements at an old DN inherit nothing. Mailboxes, protocol logins, storage and Roundcube users have independent stable identities rather than public addresses.

Users can open their own additional boxes and manage personal aliases in Mail; authorized administrators may act for others. Go displays only the current owner’s boxes, addresses and confirmed status, with a POST handoff to that Roundcube box. Its default and alias identities exclude other boxes. One-use codes never enter URLs; Go rotates short protocol grants and rechecks new authentication/submissions. Disabling or withdrawal closes access/sending while retaining receiving and history.

Mail-suffix migration preserves the box and old address; sAMAccountName rename remains unavailable. Ordinary deletion retains address ownership. Administrator conflict transfer remains pending under R-068. The company directory exposes only main public addresses and selected contact details, supporting direct/nested and primary groups. Shared sources remain phase two interfaces.

## Administrator access

There is no local administrator or independent mailbox password. Go and the portal use current IAM/AD admission. IAM outages never enable static-password fallback; existing mail logins can still exit locally. Mail checks recent authentication, target authorization and durable audit for address administration. Recovery uses ANAS stopped restoration and the existing IAM recovery entry, never manual mail-database edits or personnel-password distribution.

## All configuration parameters

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `mail.db_type` | enum (`postgres`) | — | `postgres` | `static` | `MAIL_DB_TYPE` | no | no | no | yes | `container_recreate` | Uses the PostgreSQL database interface. |
| `mail.domain_prefix` | string | — | `mail` | `static` | `MAIL_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | HTTPS subdomain prefix for Go mail management |
| `mail.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `MAIL_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | Generic OIDC binding; both supported providers still require actual acceptance |
| `mail.ingress_capacity_bytes` | int | `0..9007199254740991` | `0` | `static` | `MAIL_INGRESS_CAPACITY_BYTES` | no | no | no | yes | `container_recreate` | Explicit total cloud reservation across active/draining sources; zero allows no bound source and never purchases storage or deletes mail. |
| `mail.ingress_credentials` | string | — | `""` | `static` | `MAIL_INGRESS_CREDENTIALS` | no | no | yes | yes | `container_recreate` | Per-source reader/control/notification/routing/proxy credentials delivered only to private Go configuration. |
| `mail.ingress_sources` | string | — | `""` | `static` | `MAIL_INGRESS_SOURCES` | no | no | no | yes | `container_recreate` | Public source JSON with IDs, generation, domains, capacity, retention and optional Cloudflare rule binding; excludes credentials. |
| `mail.language` | string | — | — | `inherited` | `MAIL_LANGUAGE` | no | yes | no | yes | `container_recreate` | Eight-language default inherited from global settings and mapped to Go and Roundcube |
| `mail.outbound_passwords` | string | — | `""` | `static` | `MAIL_OUTBOUND_PASSWORDS` | no | no | yes | yes | `container_recreate` | SMTP password JSON keyed by profile ID, delivered only to private DMS runtime configuration. |
| `mail.outbound_routing` | string | — | `""` | `static` | `MAIL_OUTBOUND_ROUTING` | no | no | no | yes | `container_recreate` | Default SMTP profile and sender-domain routing JSON; passwords are supplied separately. |
| `mail.service_api_credentials_dir` | string | — | `""` | `static` | `MAIL_SERVICE_API_CREDENTIALS_DIR` | no | no | no | yes | `container_recreate` | Dedicated anasd-issued service-token directory mounted read-only. |
| `mail.service_api_socket_dir` | string | — | `""` | `static` | `MAIL_SERVICE_API_SOCKET_DIR` | no | no | no | yes | `container_recreate` | Optional dedicated anasd mail-install socket directory, mounted read-only. |
| `mail.service_api_url` | string | — | `""` | `static` | `MAIL_SERVICE_API_URL` | no | no | no | yes | `container_recreate` | Optional direct HTTPS origin of anasd for the mail installer. |
| `mail.service_api_workspace` | string | — | `""` | `static` | `MAIL_SERVICE_API_WORKSPACE` | no | no | no | yes | `container_recreate` | Workspace ID for the restricted mail installer grant. |
| `mail.webmail_domain_prefix` | string | — | `webmail` | `static` | `MAIL_WEBMAIL_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Separate Roundcube HTTPS subdomain on the same site with isolated browser sessions |

### Outbound SMTP candidate configuration

`outbound_routing` contains service endpoints, TLS mode, usernames and exact SMTP envelope-sender domain overrides. `outbound_passwords` is secret JSON keyed by service ID; use the existing managed `secrets.MAIL_OUTBOUND_PASSWORDS` input. The hook delivers passwords only to the private DMS binding, excluding Go and Roundcube.

For example, routing can be `{"default":"resend","profiles":[{"id":"resend","provider":"resend","host":"smtp.resend.com","port":587,"tls":"starttls","username":"resend"}],"domains":{}}`. Supply the matching API key separately under that service ID. Compatible SMTP uses `provider: smtp`; implicit TLS uses `tls: tls`, such as eusend on port 465. Sending domains require separate provider verification; saving configuration does not establish public readiness.

Direct delivery can also be explicitly selected, for example `{"default":"direct","profiles":[{"id":"direct","provider":"direct"}],"domains":{}}`, without upstream credentials. Direct delivery prefers TLS, permits plaintext if unsupported, and does not require peer certificate verification. Check outbound port 25, stable IP, PTR, SPF, DKIM, DMARC and actual delivery before claiming public readiness. No automatic direct fallback is configured, and route changes still require resolving the old queue. The current management page saves drafts only and cannot enable them.

Finish the old queue before changing service or domain routes. Pending messages block a route change instead of being silently resubmitted through another provider. The isolated native TLS subset has passed; the administrator Tutorial and complete cloud acceptance remain in progress.

## Persistence, recovery and limits

Administrators can open the candidate `/setup` guide from Mail home, choose Worker/R2 or a self-managed cloud relay, review prerequisites and plan limits, and save a non-secret draft. Credentials remain in the existing ANAS Secret manager; the guide accepts references only. Drafts persist in Mail PostgreSQL across browsers. Saving does not deploy cloud resources, alter existing routes or enable service. Actual cloud verification and enablement remain in progress.

DMS content, queues/control state and Webmail temporary attachments use dedicated DATA_PATH/mail bindings. Go identities/addresses/sessions and Roundcube users/preferences use their respective PostgreSQL resources. Recovery must also include AD, IAM and required secrets. Quiescence, consistency checks and invalidation of restored credentials still need complete actual verification. /readyz remains 503; starting three processes does not complete phase one.

Run go test ./modules/mail/...; actual checks require explicit isolated bindings and skipped tests establish no real-host pass.

<!-- generated:localization:start -->
<!-- generated:localization:end -->
