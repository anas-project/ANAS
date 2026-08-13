package main

import (
	"strings"
	"testing"
)

// The fetcher list is the one field with a small closed set, so a typo in it is
// worth catching before the container starts.
func TestPublicIPFetchersAreValidated(t *testing.T) {
	for _, value := range []string{"http", "dns", "all", "http,dns", " http , dns "} {
		e := map[string]string{"DDNS_UPDATER_PUBLICIP_FETCHERS": value}
		if err := validatePublicIPFetchers(e); err != nil {
			t.Errorf("%q was rejected: %v", value, err)
		}
	}
	for _, value := range []string{"https", "interface", "http,netInterface"} {
		e := map[string]string{"DDNS_UPDATER_PUBLICIP_FETCHERS": value}
		err := validatePublicIPFetchers(e)
		if err == nil {
			t.Errorf("%q was accepted", value)
			continue
		}
		if !strings.Contains(err.Error(), "http, dns, all") {
			t.Errorf("error %q does not list the accepted methods", err.Error())
		}
	}
}

// Provider names are not validated here on purpose: ddns-updater accepts
// eleven of them plus arbitrary url: endpoints, and duplicating that list would
// be a second thing to keep in step with upstream.
func TestProviderListsArePassedThroughUnchecked(t *testing.T) {
	e := map[string]string{
		"DDNS_UPDATER_PUBLICIP_FETCHERS":       "http",
		"DDNS_UPDATER_PUBLICIP_IPV4_PROVIDERS": "url:https://myip.ipip.net",
	}
	if err := validatePublicIPFetchers(e); err != nil {
		t.Fatalf("an explicit endpoint was rejected: %v", err)
	}
}

// The two DDNS modules have to agree on what an absent ipv4/ipv6 means. They did
// not: this one read an absent value as disabled and ddns_go read it as
// enabled. It only ever worked because the schema supplied an explicit "true",
// so the same config would have meant opposite things the moment that default
// changed or the key went missing.
func TestAbsentAddressFamilyMeansEnabled(t *testing.T) {
	if !wantAddressFamily(map[string]string{}, "IPV4") {
		t.Error("an absent ipv4 must mean enabled, matching the schema default")
	}
	if wantAddressFamily(map[string]string{"IPV4": "false"}, "IPV4") {
		t.Error("an explicit false must disable the family")
	}
	if !wantAddressFamily(map[string]string{"IPV6": "true"}, "IPV6") {
		t.Error("an explicit true must enable the family")
	}
}

func TestCloudflareRequiresAndRendersZoneIdentifierAndTTL(t *testing.T) {
	base := map[string]string{
		"DDNS_UPDATER_DNS_PROVIDER":             "cloudflare",
		"DDNS_UPDATER_CLOUDFLARE_DNS_API_TOKEN": "token",
		"BASE_DOMAIN":                           "example.test",
		"IPV4":                                  "true",
	}
	if err := calcDDNSUpdater(cloneMap(base), "", nil); err == nil || !strings.Contains(err.Error(), "zone_identifier") {
		t.Fatalf("missing zone identifier error = %v", err)
	}

	env := cloneMap(base)
	env["DDNS_UPDATER_ZONE_IDENTIFIER"] = "zone-id"
	env["DDNS_UPDATER_TTL"] = "300"
	if err := calcDDNSUpdater(env, "", nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"zone_identifier":"zone-id"`, `"ttl":300`} {
		if !strings.Contains(env["DDNS_UPDATER_CONFIG"], want) {
			t.Fatalf("CONFIG %s does not contain %s", env["DDNS_UPDATER_CONFIG"], want)
		}
	}
}
