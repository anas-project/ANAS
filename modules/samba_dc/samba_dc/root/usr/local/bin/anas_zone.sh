#!/usr/bin/with-contenv bash

set -euo pipefail

managed_state=/var/lib/samba/.anas-managed-dns-v1.tsv
pending_state=/var/lib/samba/.anas-managed-dns-pending-v1.tsv
zone_state=/var/lib/samba/.anas-application-zone-v1
legacy_adoption_state=/var/lib/samba/.anas-legacy-zone-adoption
state_lock=/var/lib/samba/.anas-dns.lock
desired_state=/run/anas-managed-dns-v1.tsv
credentials="${SAMBA_DC_ADMINISTRATOR_NAME}%${SAMBA_DC_ADMINISTRATOR_PASSWORD}"
mode="${SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED:-ad_zone}"
zone="${SAMBA_DC_APPLICATION_DNS_ZONE:-${SAMBA_DC_DOMAIN}}"
deployment="${ANAS_DEPLOYMENT_ID:-legacy}"
legacy_adoption=false

canonical_fqdn() {
  local value="${1%.}"
  printf '%s' "$value" | tr '[:upper:]' '[:lower:]'
}

relative_name() {
  local fqdn selected_zone
  fqdn=$(canonical_fqdn "$1")
  selected_zone=$(canonical_fqdn "$2")
  if [ "$fqdn" = "$selected_zone" ]; then
    printf '@'
    return 0
  fi
  case "$fqdn" in
    *."$selected_zone") printf '%s' "${fqdn%.${selected_zone}}" ;;
    *) return 1 ;;
  esac
}

retry() {
  local attempts="$1" message="$2"
  shift 2
  local count=1
  while ! "$@"; do
    if [ "$count" -ge "$attempts" ]; then
      echo "$message after $attempts attempts" >&2
      return 1
    fi
    count=$((count + 1))
    sleep 2
  done
}

samba_ready() {
  samba-tool domain level show >/dev/null 2>&1
}

dns_ready() {
  nc -z 127.0.0.1 53 >/dev/null 2>&1
}

zone_named_exists() {
	local selected_zone="$1" output
	if ! output=$(samba-tool dns zonelist 127.0.0.1 -U "$credentials" 2>/dev/null); then
		# Return a distinct status for an RPC/parse failure. Callers must never
		# treat an unknown zone inventory as proof that a zone is absent.
		return 2
	fi
	printf '%s\n' "$output" | awk -F ':' -v selected_zone="$selected_zone" '
      /pszZoneName/ {
		seen=1
        value=$2
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
        if (tolower(value) == tolower(selected_zone)) found=1
      }
		END {
			if (!seen) exit 2
			exit !found
		}
    '
}

zone_exists() {
  zone_named_exists "$zone"
}

dns_rpc_ready() {
  samba-tool dns zonelist 127.0.0.1 -U "$credentials" >/dev/null 2>&1
}

conflicting_child_zone() {
	local managed_fqdn output
	managed_fqdn=$(canonical_fqdn "${1:-${BASE_DOMAIN:-$SAMBA_DC_HOST}}")
	if ! output=$(samba-tool dns zonelist 127.0.0.1 -U "$credentials" 2>/dev/null); then
		return 2
	fi
	printf '%s\n' "$output" | awk -F ':' -v managed_fqdn="$managed_fqdn" -v parent_zone="$zone" '
      function within(name, suffix) {
        return name == suffix || (length(name) > length(suffix) && substr(name, length(name) - length(suffix), 1) == "." && substr(name, length(name) - length(suffix) + 1) == suffix)
      }
      /pszZoneName/ {
		seen=1
        candidate=$2
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", candidate)
        candidate=tolower(candidate)
        if (found == "" && candidate != tolower(parent_zone) && within(tolower(managed_fqdn), candidate) && within(candidate, tolower(parent_zone))) {
          found=candidate
        }
      }
		END {
			if (!seen) exit 2
			if (found != "") print found
		}
    '
}

desired_dns_fqdns() {
	local entry kind fqdn owner
	canonical_fqdn "$SAMBA_DC_HOST"
	printf '\n'
	if [ -n "${DOMAINS:-}" ]; then
		local -a desired_entries
		IFS=',' read -r -a desired_entries <<< "$DOMAINS"
		for entry in "${desired_entries[@]}"; do
			[ -n "$entry" ] || continue
			IFS='/' read -r kind fqdn owner <<< "$entry"
			[ "$kind" = inner ] || continue
			canonical_fqdn "$fqdn"
			printf '\n'
		done
	fi
}

validate_no_conflicting_child_zones() {
	local fqdn child_zone
	while IFS= read -r fqdn; do
		[ -n "$fqdn" ] || continue
		if ! child_zone=$(conflicting_child_zone "$fqdn"); then
			echo "cannot inspect Samba DNS child zones for managed name $fqdn; refusing reconciliation" >&2
			return 1
		fi
		if [ -n "$child_zone" ]; then
			echo "DNS zone $child_zone intercepts managed name $fqdn below authoritative zone $zone; migrate or remove the child zone explicitly" >&2
			return 1
		fi
	done < <(desired_dns_fqdns)
}

write_zone_state() {
	local selected_mode="$1" selected_zone="$2" ownership="$3"
	local next_zone_state="${zone_state}.tmp.$$"
	printf '%s\t%s\t%s\n' "$selected_mode" "$selected_zone" "$ownership" > "$next_zone_state" || {
		rm -f "$next_zone_state"
		return 1
	}
	mv "$next_zone_state" "$zone_state" || {
		rm -f "$next_zone_state"
		return 1
	}
}

prepare_zone() {
	local previous_mode previous_zone ownership zone_status
  if [ -f "$zone_state" ]; then
    IFS=$'\t' read -r previous_mode previous_zone ownership < "$zone_state"
    if [ "$previous_mode" != "$mode" ] || [ "$previous_zone" != "$zone" ]; then
      echo "application DNS target changed from mode=$previous_mode zone=$previous_zone to mode=$mode zone=$zone; run migrate-application-dns-zone" >&2
      return 1
    fi
  fi

	case "$mode" in
		ad_zone)
			if zone_exists; then
				:
			else
				zone_status=$?
				if [ "$zone_status" -eq 1 ]; then
					echo "AD DNS zone $zone does not exist" >&2
				else
					echo "cannot inspect Samba DNS zones; refusing to infer that AD zone $zone is absent" >&2
				fi
				return 1
			fi
			validate_no_conflicting_child_zones || return 1
      if { [ -f "$legacy_adoption_state" ] && [ ! -f "$managed_state" ]; } ||
         { [ "${ownership:-}" = directory_legacy ] && [ ! -f "$managed_state" ]; }; then
        # Compatibility bridge for records created by the pre-manifest ANAS
        # reconciler. Only an exact desired target may be adopted; conflicting
        # records still require an explicit migration/ownership decision.
        legacy_adoption=true
        ownership=directory_legacy
      else
        ownership=directory
      fi
			;;
		separate_zone)
			if zone_exists; then
				validate_no_conflicting_child_zones || return 1
				if [ ! -f "$zone_state" ]; then
          echo "separate application DNS zone $zone already exists but is not owned by ANAS" >&2
          return 1
        fi
        if [ "${ownership:-}" != anas ] && [ "${ownership:-}" != anas_pending ]; then
          echo "separate application DNS zone $zone has invalid ownership state ${ownership:-missing}" >&2
          return 1
        fi
			else
				zone_status=$?
				if [ "$zone_status" -ne 1 ]; then
					echo "cannot inspect Samba DNS zones; refusing to infer that application zone $zone is absent" >&2
					return 1
				fi
				validate_no_conflicting_child_zones || return 1
				# Persist intent before creating the zone. If the process dies after
				# Samba commits the zone but before the final state write, the next run
				# can distinguish that crash window from an unrelated existing zone.
				write_zone_state "$mode" "$zone" anas_pending
				echo "Create ANAS application DNS zone $zone"
				if ! samba-tool dns zonecreate 127.0.0.1 "$zone" -U "$credentials" >/dev/null; then
					# A failed or ambiguous create is not ownership evidence. Removing
					# the journal forces explicit recovery if Samba committed despite
					# returning an error, rather than claiming an unrelated zone later.
					rm -f "$zone_state"
					echo "failed to create ANAS application DNS zone $zone; pending ownership was withdrawn" >&2
					return 1
				fi
			fi
      ownership=anas
      ;;
    *)
      echo "unsupported resolved application DNS mode $mode" >&2
      return 1
      ;;
  esac

  write_zone_state "$mode" "$zone" "$ownership"
}

query_a_records() {
  local name="$1"
  samba-tool dns query 127.0.0.1 "$zone" "$name" A -U "$credentials" 2>/dev/null |
    awk '$1 == "A:" { print $2 }' || true
}

# A missing name and an unreachable DNS RPC endpoint both make a direct query
# non-zero. Before treating an empty result as "already removed", prove the
# same zone can still be queried; otherwise retain the ownership record and
# fail this reconciliation so a transient error can be retried safely.
query_a_records_verified() {
  local name="$1" output
  if output=$(samba-tool dns query 127.0.0.1 "$zone" "$name" A -U "$credentials" 2>/dev/null); then
    printf '%s\n' "$output" | awk '$1 == "A:" { print $2 }'
    return 0
  fi
  samba-tool dns query 127.0.0.1 "$zone" @ ALL -U "$credentials" >/dev/null 2>&1 || return 1
  return 0
}

state_record_target() {
	local state_file="$1" fqdn="$2"
	[ -f "$state_file" ] || return 0
	awk -F '\t' -v selected_zone="$zone" -v selected_fqdn="$fqdn" '
    $1 == selected_zone && $2 == selected_fqdn && $3 == "A" { print $4; exit }
  ' "$state_file"
}

old_managed_target() {
  local fqdn="$1"
	state_record_target "$managed_state" "$fqdn"
}

pending_managed_target() {
	local fqdn="$1"
	state_record_target "$pending_state" "$fqdn"
}

legacy_observed_target() {
	local fqdn="$1"
	[ -f "$managed_state" ] || return 0
	awk -F '\t' -v selected_zone="$zone" -v selected_fqdn="$fqdn" '
    $1 == selected_zone && $2 == selected_fqdn && $3 == "A_LEGACY" { print $4; exit }
  ' "$managed_state"
}

# Persist ownership intent before creating a previously absent record. This
# closes the add-before-manifest crash window: a restart can adopt or clean up
# only the exact record named by this journal, while unrelated records remain
# unclaimable.
record_pending_intent() {
	local fqdn="$1" target="$2" owner="$3" next_pending="${pending_state}.tmp.$$"
	if [ -f "$pending_state" ]; then
		awk -F '\t' -v selected_zone="$zone" -v selected_fqdn="$fqdn" '
      !($1 == selected_zone && $2 == selected_fqdn && $3 == "A")
		' "$pending_state" > "$next_pending" || {
			rm -f "$next_pending"
			return 1
		}
	else
		: > "$next_pending" || return 1
	fi
	printf '%s\t%s\tA\t%s\t%s\t%s\n' "$zone" "$fqdn" "$target" "$owner" "$deployment" >> "$next_pending" || {
		rm -f "$next_pending"
		return 1
	}
	mv "$next_pending" "$pending_state" || {
		rm -f "$next_pending"
		return 1
	}
}

remove_pending_intent() {
	local fqdn="$1" target="$2" next_pending="${pending_state}.tmp.$$"
	[ -f "$pending_state" ] || return 0
	awk -F '\t' -v selected_zone="$zone" -v selected_fqdn="$fqdn" -v selected_target="$target" '
    !($1 == selected_zone && $2 == selected_fqdn && $3 == "A" && $4 == selected_target)
	' "$pending_state" > "$next_pending" || {
		rm -f "$next_pending"
		return 1
	}
	if [ -s "$next_pending" ]; then
		mv "$next_pending" "$pending_state" || {
			rm -f "$next_pending"
			return 1
		}
	else
		rm -f "$next_pending" || return 1
		rm -f "$pending_state" || return 1
	fi
}

observed_a_records_are_proven() {
	local records="$1" managed_target="$2" journaled_target="$3" value
	while IFS= read -r value; do
		[ -n "$value" ] || continue
		if [ "$value" != "$managed_target" ] && [ "$value" != "$journaled_target" ]; then
			return 1
		fi
	done <<< "$records"
}

ensure_managed_a_record() {
  local fqdn="$1" target="$2" owner="$3" name existing old_target pending_target legacy_target known_target record_type
  name=$(relative_name "$fqdn" "$zone") || {
    echo "managed FQDN $fqdn is outside authoritative zone $zone" >&2
    return 1
  }
  existing=$(query_a_records_verified "$name") || {
    echo "cannot query DNS record $fqdn; refusing to change ownership state" >&2
    return 1
  }
	old_target=$(old_managed_target "$fqdn") || {
		echo "cannot read managed DNS ownership state for $fqdn" >&2
		return 1
	}
	pending_target=$(pending_managed_target "$fqdn") || {
		echo "cannot read pending DNS ownership state for $fqdn" >&2
		return 1
	}
	legacy_target=$(legacy_observed_target "$fqdn") || {
		echo "cannot read legacy DNS observation state for $fqdn" >&2
		return 1
	}
	if [ -n "$legacy_target" ]; then
		if [ "$legacy_target" != "$target" ] || [ "$existing" != "$target" ]; then
			echo "legacy-observed DNS record $fqdn is not deletion-managed by ANAS and no longer matches target $target; resolve ownership explicitly" >&2
			return 1
		fi
		echo "Observe legacy DNS record without taking deletion ownership fqdn=$fqdn target=$target"
		printf '%s\t%s\tA_LEGACY\t%s\t%s\t%s\n' "$zone" "$fqdn" "$target" "$owner" "$deployment" >> "$desired_state"
		return 0
	fi
  known_target="$old_target"
  if [ -z "$known_target" ]; then
    known_target="$pending_target"
  fi

	echo "Reconcile DNS mode=$mode fqdn=$fqdn zone=$zone relative=$name target=$target owner=$owner"
	record_type=A
	if [ -z "$known_target" ] && [ -n "$existing" ]; then
    if [ "$legacy_adoption" = true ] && printf '%s\n' "$existing" | grep -Fxq "$target"; then
		echo "Observe legacy DNS record $fqdn target=$target without taking deletion ownership"
		record_type=A_LEGACY
    elif printf '%s\n' "$existing" | grep -Fxq "$target"; then
      echo "DNS record $fqdn already exists but is not in the ANAS managed-resource state" >&2
      return 1
    else
      echo "DNS name $fqdn has unmanaged A records and cannot be claimed by ANAS" >&2
      return 1
    fi
  fi
	# Once ANAS owns a name, every observed value must still have provenance:
	# either the committed manifest or a durable mutation journal. In
	# particular, an administrator-created desired target must not be claimed
	# merely because a replacement happens to want the same address.
	if [ -n "$known_target" ] && ! observed_a_records_are_proven "$existing" "$old_target" "$pending_target"; then
		echo "DNS name $fqdn contains A records not proven by ANAS managed or pending state; refusing ownership promotion" >&2
		return 1
	fi

	# A pending target is an exact, durable proof of a mutation started by ANAS.
	# If an activation is compensating back to the previously managed target,
	# remove only that journaled value before restoring the old desired record.
	if [ -n "$pending_target" ] && [ "$pending_target" != "$target" ] && printf '%s\n' "$existing" | grep -Fxq "$pending_target"; then
		samba-tool dns delete 127.0.0.1 "$zone" "$name" A "$pending_target" -U "$credentials" >/dev/null || return 1
		existing=$(query_a_records_verified "$name") || {
			echo "cannot verify interrupted DNS target $pending_target for $fqdn; retaining pending ownership state" >&2
			return 1
		}
	fi
	# Journal both initial creation and A->B replacement before the first DNS
	# mutation. Exact legacy observations do not gain ownership merely because
	# their target already matches.
	if [ "$known_target" != "$target" ] && [ "$pending_target" != "$target" ]; then
		if [ -n "$known_target" ] || ! printf '%s\n' "$existing" | grep -Fxq "$target"; then
			record_pending_intent "$fqdn" "$target" "$owner" || {
				echo "cannot persist pending DNS ownership intent for $fqdn; refusing DNS mutation" >&2
				return 1
			}
			pending_target="$target"
		fi
	fi
  if [ -n "$known_target" ] && [ "$known_target" != "$target" ] && printf '%s\n' "$existing" | grep -Fxq "$known_target"; then
		if ! samba-tool dns delete 127.0.0.1 "$zone" "$name" A "$known_target" -U "$credentials" >/dev/null; then
			remove_pending_intent "$fqdn" "$target" || echo "cannot withdraw failed DNS replacement intent for $fqdn" >&2
			return 1
		fi
    existing=$(query_a_records_verified "$name") || {
      echo "cannot verify whether managed DNS record $fqdn still exists; retaining ownership state" >&2
      return 1
    }
  fi
  if ! printf '%s\n' "$existing" | grep -Fxq "$target"; then
		if ! samba-tool dns add 127.0.0.1 "$zone" "$name" A "$target" -U "$credentials" >/dev/null; then
			# A non-zero result is not proof that ANAS created the value. Withdraw
			# the intent so a subsequent run cannot claim a concurrent manual A.
			remove_pending_intent "$fqdn" "$target" || echo "cannot withdraw failed DNS add intent for $fqdn" >&2
			return 1
		fi
  fi
	existing=$(query_a_records_verified "$name") || {
		echo "cannot verify managed DNS record $fqdn after reconciliation; retaining ownership state" >&2
		return 1
	}
	if [ "$existing" != "$target" ]; then
		echo "DNS name $fqdn must contain exactly the managed A target $target; found: ${existing:-none}" >&2
		return 1
	fi
	printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$zone" "$fqdn" "$record_type" "$target" "$owner" "$deployment" >> "$desired_state"
}

desired_has_record() {
  local selected_zone="$1" fqdn="$2" record_type="$3" target="$4"
	awk -F '\t' -v selected_zone="$selected_zone" -v selected_fqdn="$fqdn" -v selected_type="$record_type" -v selected_target="$target" '
    $1 == selected_zone && $2 == selected_fqdn && $3 == selected_type && $4 == selected_target { found=1 }
    END { exit !found }
  ' "$desired_state"
}

is_directory_native_alias() {
	local selected_zone fqdn samba_domain dc_fqdn
	selected_zone=$(canonical_fqdn "$1")
	fqdn=$(canonical_fqdn "$2")
	samba_domain=$(canonical_fqdn "$SAMBA_DC_DOMAIN")
	dc_fqdn=$(canonical_fqdn "${SAMBA_DC_DC_DOMAIN:-}")
	[ "$mode" = ad_zone ] && [ "$selected_zone" = "$samba_domain" ] && {
		[ "$fqdn" = "$samba_domain" ] || { [ -n "$dc_fqdn" ] && [ "$fqdn" = "$dc_fqdn" ]; }
	}
}

ensure_directory_native_alias() {
	local fqdn="$1" target="$2" name existing
	name=$(relative_name "$fqdn" "$zone") || return 1
	existing=$(query_a_records_verified "$name") || return 1
	if ! printf '%s\n' "$existing" | grep -Fxq "$target"; then
		return 1
	fi
	echo "Observe Samba directory-native DNS record fqdn=$fqdn zone=$zone target=$target"
	printf '%s\t%s\tA_NATIVE\t%s\t%s\t%s\n' "$zone" "$fqdn" "$target" samba_directory "$deployment" >> "$desired_state"
}

delete_removed_managed_records() {
  local old_zone fqdn record_type target owner old_deployment name existing state_file
	for state_file in "$managed_state" "$pending_state"; do
		[ -f "$state_file" ] || continue
		while IFS=$'\t' read -r old_zone fqdn record_type target owner old_deployment; do
			[ -n "$old_zone" ] || continue
			if desired_has_record "$old_zone" "$fqdn" "$record_type" "$target"; then
				continue
			fi
			# Earlier ANAS versions could claim an AD-zone apex or canonical DC A
			# that Samba owns natively. Drop that stale ownership row without
			# deleting the record.
			if is_directory_native_alias "$old_zone" "$fqdn"; then
				continue
			fi
			if [ "$record_type" = A_LEGACY ]; then
				echo "Release legacy DNS observation without deleting fqdn=$fqdn zone=$old_zone target=$target"
				continue
			fi
			if [ "$old_zone" != "$zone" ] || [ "$record_type" != A ]; then
				echo "managed DNS state contains migration target zone=$old_zone fqdn=$fqdn; run migrate-application-dns-zone" >&2
				return 1
			fi
			name=$(relative_name "$fqdn" "$old_zone") || return 1
			echo "Delete removed ANAS DNS record fqdn=$fqdn zone=$old_zone relative=$name target=$target owner=$owner"
			existing=$(query_a_records_verified "$name") || {
				echo "cannot verify whether removed managed DNS record $fqdn still exists; retaining ownership state" >&2
				return 1
			}
			if printf '%s\n' "$existing" | grep -Fxq "$target"; then
				samba-tool dns delete 127.0.0.1 "$old_zone" "$name" A "$target" -U "$credentials" >/dev/null
			fi
		done < "$state_file"
	done
}

verify_a_record() {
  local fqdn="$1" target="$2"
  host "$fqdn" 127.0.0.1 2>/dev/null | grep -Fq "has address $target"
}

build_desired_records() {
  : > "$desired_state"
  # BASE_DOMAIN is the certificate-covered LDAPS service alias. It may use a
  # distinct host-LAN address, so it is intentionally not part of DOMAINS'
  # Web-service-to-HOST_IP protocol.
  local ldap_fqdn
  ldap_fqdn=$(canonical_fqdn "$SAMBA_DC_HOST")
	if is_directory_native_alias "$zone" "$ldap_fqdn"; then
		retry 30 "Samba directory-native DNS record $ldap_fqdn did not publish $SAMBA_DC_HOST_IP" \
			ensure_directory_native_alias "$ldap_fqdn" "$SAMBA_DC_HOST_IP"
	else
		retry 30 "failed to reconcile LDAPS DNS alias $ldap_fqdn" \
			ensure_managed_a_record "$ldap_fqdn" "$SAMBA_DC_HOST_IP" samba_dc
	fi

  local entry kind fqdn owner
  local -a entries
  if [ -n "${DOMAINS:-}" ]; then
    IFS=',' read -r -a entries <<< "$DOMAINS"
    for entry in "${entries[@]}"; do
      [ -n "$entry" ] || continue
      IFS='/' read -r kind fqdn owner <<< "$entry"
      case "$kind" in
        inner)
          fqdn=$(canonical_fqdn "$fqdn")
          retry 30 "failed to reconcile application DNS record $fqdn" \
            ensure_managed_a_record "$fqdn" "$HOST_IP" "$owner"
          ;;
        dhcp) echo "dhcp DNS registration is not implemented" ;;
        *) echo "unsupported DOMAINS entry $entry" >&2; return 1 ;;
      esac
    done
  fi
}

main() {
  exec 9>"$state_lock"
  flock -x 9
  retry 60 "Samba AD did not become ready" samba_ready
  retry 60 "Samba DNS RPC did not become ready" dns_rpc_ready
  prepare_zone
  build_desired_records
  delete_removed_managed_records

  local next_state="${managed_state}.tmp.$$"
  cp "$desired_state" "$next_state"
  mv "$next_state" "$managed_state"
	rm -f "$pending_state"
  rm -f "$legacy_adoption_state"

  # Publish the local AD database only after zone and record reconciliation.
  touch /var/lib/samba/.anas-zone-ready
  retry 60 "DNS service did not become ready" dns_ready
  printf 'nameserver 127.0.0.1\n' > /etc/resolv.conf

  local managed_zone fqdn record_type target owner applied
  while IFS=$'\t' read -r managed_zone fqdn record_type target owner applied; do
    retry 30 "DNS readiness failed for $fqdn" verify_a_record "$fqdn" "$target"
  done < "$managed_state"
  echo "Application DNS reconciliation completed: mode=$mode zone=$zone"
  touch /run/anas-zone.ready
}

if [ "${ANAS_ZONE_LIB_ONLY:-false}" != true ]; then
  main "$@"
fi
