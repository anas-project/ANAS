ARG GHCR_REGISTRY=ghcr.io
FROM ${GHCR_REGISTRY}/goauthentik/server:2026.5.6@sha256:ed120caf710ccf82ef0026f0bc74e51615bc95ebff228a7a2d6fc60c441c3868

# The upstream runtime user is intentionally unprivileged. Elevate only while
# applying the deterministic source patch, then restore the upstream UID/GID so
# the released image never runs authentik as root.
USER root
COPY patch-password-writeback-errors.py /opt/anas/build/patch-password-writeback-errors.py
COPY patch-canonical-oidc-sub.py /opt/anas/build/patch-canonical-oidc-sub.py
COPY verify-canonical-oidc-sub.py /opt/anas/build/verify-canonical-oidc-sub.py
COPY patch-directory-admission.py /opt/anas/build/patch-directory-admission.py
COPY verify-directory-admission.py /opt/anas/build/verify-directory-admission.py
COPY --chmod=0444 directory_admission.py /authentik/sources/ldap/anas_admission.py
RUN python3 /opt/anas/build/patch-password-writeback-errors.py /authentik \
  && python3 /opt/anas/build/patch-canonical-oidc-sub.py /authentik \
  && python3 /opt/anas/build/patch-directory-admission.py /authentik \
  && python3 /opt/anas/build/verify-canonical-oidc-sub.py /authentik \
  && python3 /opt/anas/build/verify-directory-admission.py /authentik \
  && rm -f /opt/anas/build/patch-password-writeback-errors.py /opt/anas/build/patch-canonical-oidc-sub.py /opt/anas/build/verify-canonical-oidc-sub.py /opt/anas/build/patch-directory-admission.py /opt/anas/build/verify-directory-admission.py
USER 1000:1000

# Keep ANAS-specific runtime code in the release image. Deployment-specific
# applications, credentials, and directory values remain generated blueprints
# and environment variables mounted at runtime.
COPY --chmod=0755 directory_watch.py /opt/anas/bin/directory-watch
COPY --chmod=0755 worker-entrypoint.sh /opt/anas/bin/worker-entrypoint
COPY --chmod=0444 blueprints-ready.py /opt/anas/bin/blueprints-ready.py
COPY --chmod=0755 wait-blueprints.sh /opt/anas/bin/wait-blueprints
COPY --chmod=0755 server-entrypoint.sh /opt/anas/bin/server-entrypoint
