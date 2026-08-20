#!/bin/sh

set -eu

required() {
  eval 'value=${'"$1"'-}'
  if [ -z "$value" ]; then
    echo "missing required environment variable: $1" >&2
    exit 1
  fi
}

# A literal newline, needed because command substitution strips trailing ones
# and so cannot be used to test for an embedded newline.
NL=$(printf '\nx'); NL=${NL%x}
CR=$(printf '\rx'); CR=${CR%x}

# yaml_string renders an arbitrary value as a double-quoted YAML scalar.
#
# Route rules and upstream URLs are structured expressions -- backticks,
# parentheses, colons, slashes -- so they cannot be constrained to a narrow
# alphabet the way certificate basenames are. Quoting plus escaping covers
# every printable character.
#
# It deliberately does not validate: it is always called from inside a command
# substitution, where an exit would end only the subshell and let generation
# carry on with the offending value. Line breaks are rejected by
# reject_line_breaks in the main shell before any of this runs.
yaml_string() {
  printf '"%s"' "$(printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g')"
}

# reject_line_breaks refuses the one class of value that can escape a quoted
# scalar: a newline ends the scalar and everything after it becomes YAML.
reject_line_breaks() {
  case "$2" in
    *"$NL"*|*"$CR"*)
      printf 'traefik route %s must not contain a line break\n' "$1" >&2
      exit 1
      ;;
  esac
}

# route_value reads one field of a route declaration.
route_value() {
  eval 'printf "%s" "${ANAS_TRAEFIK_ROUTE__'"$1"'__'"$2"'-}"'
}

required LEGO_CERT_NAME
required LEGO_KEY_NAME

# These values are certificate basenames, not arbitrary paths or YAML. Keeping
# the accepted alphabet narrow prevents path traversal and structured-data
# injection without adding a template engine to the image.
for value in "$LEGO_CERT_NAME" "$LEGO_KEY_NAME"; do
  case "$value" in
    *[!A-Za-z0-9._-]*|.*|*..*)
      echo "certificate names must be simple basenames" >&2
      exit 1
      ;;
  esac
done

umask 077
config_dir=${ANAS_CONFIG_DIR:-/run/anas}
mkdir -p "$config_dir"
cat > "$config_dir/cert.yml.tmp" <<EOF
tls:
  certificates:
    - certFile: /certs/$LEGO_CERT_NAME
      keyFile: /certs/$LEGO_KEY_NAME
      stores:
        - default
EOF
mv "$config_dir/cert.yml.tmp" "$config_dir/cert.yml"

# Declared routes. The Docker provider can only see containers that share
# Traefik's network, which leaves out anything on host networking, anything
# outside Docker, and anything Traefik must reach by address rather than by
# container. Those services register here instead:
#
#   ANAS_TRAEFIK_ROUTE__<NAME>__RULE          required, Traefik rule expression
#   ANAS_TRAEFIK_ROUTE__<NAME>__URL           required, upstream base URL
#   ANAS_TRAEFIK_ROUTE__<NAME>__MIDDLEWARES   optional, comma-separated
#   ANAS_TRAEFIK_ROUTE__<NAME>__ENTRYPOINTS   optional, defaults to https
#   ANAS_TRAEFIK_ROUTE__<NAME>__TLS           optional, defaults to true
#
# <NAME> is restricted to the characters an environment variable may hold, so
# it needs no separate validation; the enumeration pattern below is the check.
routes=$(env | sed -n 's/^ANAS_TRAEFIK_ROUTE__\([A-Za-z0-9_]*\)__RULE=.*/\1/p' | sort -u)

# Validate every field first, in this shell, so a rejection actually stops the
# container from starting with a half-written route file.
for route in $routes; do
  for field in RULE URL MIDDLEWARES ENTRYPOINTS TLS; do
    reject_line_breaks "$route.$field" "$(route_value "$route" "$field")"
  done
  if [ -z "$(route_value "$route" URL)" ]; then
    printf 'traefik route %s declares a rule but no upstream URL\n' "$route" >&2
    exit 1
  fi
done

if [ -n "$routes" ]; then
  # printf throughout, never echo: in POSIX sh echo expands backslash escapes,
  # which would silently undo the escaping yaml_string just applied.
  {
    printf 'http:\n  routers:\n'
    for route in $routes; do
      name=$(printf '%s' "$route" | tr 'A-Z_' 'a-z-')
      rule=$(route_value "$route" RULE)
      entrypoints=$(route_value "$route" ENTRYPOINTS)
      [ -n "$entrypoints" ] || entrypoints=https
      middlewares=$(route_value "$route" MIDDLEWARES)
      tls=$(route_value "$route" TLS)
      [ -n "$tls" ] || tls=true

      printf '    %s:\n' "$name"
      printf '      rule: %s\n' "$(yaml_string "$rule")"
      printf '      service: %s\n' "$name"
      printf '      entryPoints:\n'
      # Splitting on commas is why a list field cannot itself contain one.
      IFS=','
      for entrypoint in $entrypoints; do
        [ -n "$entrypoint" ] || continue
        printf '        - %s\n' "$(yaml_string "$entrypoint")"
      done
      if [ -n "$middlewares" ]; then
        printf '      middlewares:\n'
        for middleware in $middlewares; do
          [ -n "$middleware" ] || continue
          printf '        - %s\n' "$(yaml_string "$middleware")"
        done
      fi
      unset IFS
      if [ "$tls" = "true" ]; then
        printf '      tls: {}\n'
      fi
    done
    printf '  services:\n'
    for route in $routes; do
      name=$(printf '%s' "$route" | tr 'A-Z_' 'a-z-')
      printf '    %s:\n      loadBalancer:\n        servers:\n' "$name"
      printf '          - url: %s\n' "$(yaml_string "$(route_value "$route" URL)")"
    done
  } > "$config_dir/routes.yml.tmp"
  mv "$config_dir/routes.yml.tmp" "$config_dir/routes.yml"
else
  # A stale file from a previous release would keep advertising a route the
  # deployment no longer declares.
  rm -f "$config_dir/routes.yml"
fi

# Traefik always derives forwarded headers for a direct client. Preserve an
# incoming X-Forwarded-* chain only when the connection came from an explicitly
# configured upstream proxy; insecure mode is intentionally never enabled.
if [ -n "${TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS:-}" ]; then
  set -- "$@" "--entrypoints.https.forwardedHeaders.trustedIPs=${TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS}"
fi

exec "${ANAS_TRAEFIK_BINARY:-traefik}" "$@"
