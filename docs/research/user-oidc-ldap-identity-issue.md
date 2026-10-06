> Draft only; not submitted. Our fresh-deployment use case passed on 2026-10-05 by configuring the LDAP internal username attribute to the immutable directory attribute, setting OIDC UID mapping to `sub`, and adding the directory attribute to LDAP search attributes and the login filter. The request below remains relevant only when an existing different internal UID must be preserved.

## Feature request

Could user_oidc support resolving an existing user_ldap account using a separately configured immutable directory claim, while preserving that account's existing Nextcloud UID?

Related: #507 (matching LDAP internal usernames), #547 (username changes), and #1498 (provider/subject lookup for user_oidc accounts). This request specifically concerns existing LDAP accounts with `auto_provision=false`, rather than accounts provisioned into user_oidc's own backend. If this belongs under an existing issue, please let me know.

## Environment and configuration

- Nextcloud 34.0.2, Docker Apache deployment
- user_oidc 8.10.1, user_ldap enabled
- Samba AD directory, Casdoor OIDC provider
- `user_oidc.auto_provision=false`
- OIDC UID mapping: `preferred_username`
- LDAP internal username derived from `sAMAccountName` when the user is first mapped
- LDAP UUID attribute and a signed OIDC claim use the same immutable directory identity

The initial OIDC UID claim matches the LDAP internal UID, as required by the documented configuration. After a directory rename, the current login name changes, while Nextcloud retains the existing internal UID and file ownership.

## Reproduction on 8.10.1

1. Create a directory account with login name `alice-old` and immutable directory identity `D`.
2. Import/map it through user_ldap. Confirm its Nextcloud internal UID is `alice-old`.
3. Log in through OIDC with `preferred_username=alice-old`; create a file.
4. Rename the same directory object to `alice-new`, keeping identity `D` unchanged.
5. Log in again with `preferred_username=alice-new` and the same immutable identity claim.

The OIDC callback returns HTTP 400, “Failed to provision the user”. The existing LDAP account still has internal UID `alice-old`.

## Expected optional behavior

An explicitly configured directory identity claim could resolve `D` through the existing LDAP UUID-to-UID mapping, allowing login to the original account and preserving its files. This should not require renaming Nextcloud's internal UID, creating another account, or maintaining a second identity mapping in the IdP.

Missing or unknown identities should fail closed. A different directory object reusing `alice-old` or `alice-new` must not obtain access to the original account. Existing deleted-user and authorization checks should still apply. Mapping by email or a mutable username would not meet this requirement.

## Source investigation and local experiment

In v8.10.1, LoginController derives `$userId` from the UID mapping claim, performs LDAP search/sync, then calls `$this->userManager->get($userId)`. With auto-provisioning disabled, it uses that result directly.

A local experiment resolved the explicitly configured immutable claim through `OCA\User_LDAP\Mapping\UserMapping::getNameByUUID()` before the existing user lookup. In the isolated test, login after rename returned to the original UID and the original file remained accessible. This demonstrates the desired behavior; it is not a proposed upstream-ready patch, and that LDAP class may not be a supported cross-app API.

I also inspected v8.11.0. The LDAP callback path still performs the same direct UID lookup when `auto_provision=false`. #1498 adds a lookup in user_oidc's own account mapper, which that path does not invoke. I have **not** reproduced this on a running v8.11.0 installation yet.

Is there an existing supported configuration or API that resolves this case? If not, would an optional immutable directory claim for LDAP account resolution be appropriate?

This issue text was prepared with AI assistance; the 8.10.1 behavior and local experiment were checked against an isolated running deployment.
