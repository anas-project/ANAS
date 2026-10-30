# Native LLNG configuration, applied both to a fresh template and to persisted
# configurations. Password binds use the login name; discoverable credentials
# return the persistent session's immutable directory anchor.
if (env.SAMBA_DC_USER_CLASS_FILTER // "") == "" or (env.SAMBA_DC_USER_ENABLED_FILTER // "") == "" then
  error("Samba user class and enabled filters are required for Passkey")
else . end
| .authentication = "Choice"
| .userDB = "Same"
| .passwordDB = "Choice"
| .authChoiceModules = {
    "1_Password": "AD;AD;AD",
    "2_Passkey": ("WebAuthn;AD;AD;;;" + ({AuthLDAPFilter:
      ("(&" + env.SAMBA_DC_USER_CLASS_FILTER + env.SAMBA_DC_USER_ENABLED_FILTER
       + "(anasIdentityAnchor=$user))")} | tojson))
  }
| .authChoiceAuthBasic = "1_Password"
| .whatToTrace = "anasIdentityAnchor"
# Two workers must see device updates and revoked sessions immediately. Native
# per-process caching can otherwise overwrite a just-registered backup key.
| .handlerInternalCache = 0
| .localSessionStorage = ""
| .tokenUseGlobalStorage = 1
# Use native signed AMR claims to tell consumers which first factor succeeded.
# Authentication level alone cannot distinguish an AD password from a Passkey.
| .oidcServiceMetaDataAmrRules += {
    webauthn: "$_auth eq \"WebAuthn\"",
    pwd: "$_auth eq \"AD\""
  }
| .ldapCAFile = ("/certs/" + (env.ANAS_TLS_TRUST_BUNDLE_NAME // env.ANAS_TLS_INTERNAL_CA_NAME // "anas-internal-ca.crt"))
| .portalUserAttr = "displayName"
| .portalCustomCss = "anas/theme.css"
| .portalCustomJs = "anas/theme.js"
| .portalMainLogo = (env.LLNG_BRAND_LOGO // "anas/logo.svg")
| .portalFavicon = .portalMainLogo
| .portalSkinBackground = ""
| .portalDisplayChangePassword = 1
| .ldapExportedVars += {
    anasIdentityAnchor: "anasIdentityAnchor",
    anasAccountControl: "userAccountControl",
    anasAccountState: "msDS-User-Account-Control-Computed",
    anasAccountExpires: "accountExpires"
  }
# Password age and pwdLastSet=0 deliberately do not appear in these rules.
# Those states apply to the password bind, not a verified Passkey assertion.
| .grantSessionRules["##ANAS-directory-identity"] =
    "defined($anasIdentityAnchor) and $anasIdentityAnchor =~ /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i"
| .grantSessionRules["##ANAS-directory-status"] =
    "defined($anasAccountControl) and $anasAccountControl =~ /^[0-9]+$/ and (($anasAccountControl & 2) == 0) and defined($anasAccountState) and $anasAccountState =~ /^([0-9]+)(?:;\\s*\\1)*$/ and (($1 & 16) == 0) and defined($anasAccountExpires) and $anasAccountExpires =~ /^[0-9]+$/ and defined($_lastAuthnUTime) and ($anasAccountExpires == 0 or $anasAccountExpires > ($_lastAuthnUTime + 11644473600) * 10000000)"
| .webauthn2fSelfRegistration = 1
# These are alternative first factors, not a new mandatory second factor.
# A working AD password remains the recovery path after losing every device.
# The native device manager also checks that this rule is a nonempty string.
# A false expression keeps removal available without requiring a second factor.
| .webauthn2fActivation = "0 == 1"
| .webauthn2fUserCanRemoveKey = 1
| .webauthn2fResidentKey = "required"
| .webauthn2fUserVerification = "required"
| .webauthnDisplayNameAttr = "displayName"
| .webauthnRpName = env.SERVER_NAME
| .customPlugins = (((.customPlugins // "" | split(",") | map(select(length > 0)))
    + ["Lemonldap::NG::Portal::Plugins::AnasOIDC"]) | unique | join(","))
| if (env.LLNG_REAUTH_CLIENT_IDS // "") != "" then
    .customPluginsParams.anasReauthClients = env.LLNG_REAUTH_CLIENT_IDS
  else del(.customPluginsParams.anasReauthClients) end
| if (env.LLNG_ACCOUNT_CLIENT_IDS // "") != "" then
    .customPluginsParams.anasAccountClients = env.LLNG_ACCOUNT_CLIENT_IDS
  else del(.customPluginsParams.anasAccountClients) end
| if (env.LLNG_REAUTH_PARENT_CLIENTS // "{}") != "{}" then
    .customPluginsParams.anasReauthParents = env.LLNG_REAUTH_PARENT_CLIENTS
  else del(.customPluginsParams.anasReauthParents) end
| .skipRenewConfirmation = 1
| if (env.LLNG_BACKGROUND_LOGOUT_EXCEPTIONS // "") != "" then
    .customPluginsParams.anasBackgroundLogoutExceptions = env.LLNG_BACKGROUND_LOGOUT_EXCEPTIONS
  else del(.customPluginsParams.anasBackgroundLogoutExceptions) end
