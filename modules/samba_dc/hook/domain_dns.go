package main

import (
	"fmt"
	"strings"
)

type domainDNSPlan struct {
	BaseDomain    string
	SambaDomain   string
	RequestedMode string
	ResolvedMode  string
	Zone          string
}

// validateDomainDNSConfig is the single desired-state interpretation used by
// both the validate and calculate phases. Core has already applied dns_name and
// enum normalization; the light canonicalization here keeps direct Hook tests
// and frozen legacy deployments deterministic without duplicating the full
// schema implementation.
func validateDomainDNSConfig(env map[string]string) (domainDNSPlan, error) {
	baseDomain := canonicalDomain(env["BASE_DOMAIN"])
	if baseDomain == "" {
		return domainDNSPlan{}, fmt.Errorf("BASE_DOMAIN is required")
	}
	sambaDomain := canonicalDomain(env["SAMBA_DC_DOMAIN"])
	if sambaDomain == "" {
		sambaDomain = baseDomain
	}
	requestedMode := strings.ToLower(strings.TrimSpace(env["SAMBA_DC_APPLICATION_DNS_MODE"]))
	if requestedMode == "" {
		requestedMode = "auto"
	}
	equalOrSubdomain := baseDomain == sambaDomain || strings.HasSuffix(baseDomain, "."+sambaDomain)

	resolvedMode := requestedMode
	switch requestedMode {
	case "auto":
		if equalOrSubdomain {
			resolvedMode = "ad_zone"
		} else {
			resolvedMode = "separate_zone"
		}
	case "ad_zone":
		if !equalOrSubdomain {
			return domainDNSPlan{}, fmt.Errorf(
				"application_dns_mode=ad_zone requires BASE_DOMAIN to equal or be a DNS-label subdomain of SAMBA_DC_DOMAIN; got BASE_DOMAIN=%s, SAMBA_DC_DOMAIN=%s; use application_dns_mode=separate_zone or auto",
				baseDomain, sambaDomain,
			)
		}
	case "separate_zone":
		if baseDomain == sambaDomain {
			return domainDNSPlan{}, fmt.Errorf(
				"application_dns_mode=separate_zone cannot create an application zone with the same name as the existing AD zone %s; use application_dns_mode=ad_zone or auto",
				sambaDomain,
			)
		}
	default:
		return domainDNSPlan{}, fmt.Errorf("application_dns_mode must be auto, ad_zone or separate_zone, got %q", requestedMode)
	}

	wantRealm := strings.ToUpper(sambaDomain)
	if realm := strings.TrimSuffix(strings.TrimSpace(env["SAMBA_DC_REALM"]), "."); realm != "" && !strings.EqualFold(realm, wantRealm) {
		return domainDNSPlan{}, fmt.Errorf("SAMBA_DC_REALM must equal upper(SAMBA_DC_DOMAIN); got SAMBA_DC_REALM=%s, SAMBA_DC_DOMAIN=%s", realm, sambaDomain)
	}
	zone := sambaDomain
	if resolvedMode == "separate_zone" {
		zone = baseDomain
	}
	return domainDNSPlan{
		BaseDomain: baseDomain, SambaDomain: sambaDomain,
		RequestedMode: requestedMode, ResolvedMode: resolvedMode, Zone: zone,
	}, nil
}

func canonicalDomain(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}
