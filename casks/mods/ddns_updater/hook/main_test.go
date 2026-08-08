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
