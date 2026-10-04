package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Temporary second client uses the same signing configuration and a distinct audience.
func peerApplication(config config, args []string, remove bool) error {
	flags := flag.NewFlagSet("peer-application", flag.ContinueOnError)
	source := flags.String("from", "app-anas-nextcloud", "source application")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if !strings.HasPrefix(config.application, "app-anas-e2e-peer-") {
		return errors.New("peer operation requires the dedicated test application prefix")
	}
	admin, err := adminClient(config)
	if err != nil {
		return err
	}
	name := *source
	if remove {
		name = config.application
	}
	var envelope struct {
		Status string                 `json:"status"`
		Data   map[string]interface{} `json:"data"`
	}
	if _, err := getJSON(admin, config.issuer+"/api/get-application?id="+url.QueryEscape("admin/"+name), &envelope); err != nil {
		return err
	}
	if envelope.Status != "ok" || envelope.Data == nil {
		return errors.New("source application is unavailable")
	}
	application := envelope.Data
	action := "delete-application"
	if !remove {
		action = "add-application"
		application["name"] = config.application
		application["clientId"] = config.clientID
		application["clientSecret"] = config.clientSecret
		application["redirectUris"] = []string{config.redirectURI}
		application["backchannelLogoutUri"] = config.backchannelURI
	}
	var result struct {
		Status string `json:"status"`
	}
	response, err := doJSON(admin, http.MethodPost, config.issuer+"/api/"+action, application, &result)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || result.Status != "ok" {
		return fmt.Errorf("%s failed: HTTP %d", action, response.StatusCode)
	}
	return nil
}
