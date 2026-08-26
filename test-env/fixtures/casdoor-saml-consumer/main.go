package main

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/crewjam/saml"
	"github.com/crewjam/saml/samlsp"
)

type stringList []string

func (values *stringList) String() string { return strings.Join(*values, ",") }
func (values *stringList) Set(value string) error {
	*values = append(*values, value)
	return nil
}

type requestOutput struct {
	ID      string `json:"id"`
	Request string `json:"request"`
}

type assertionOutput struct {
	Issuer       string              `json:"issuer"`
	Destination  string              `json:"destination"`
	InResponseTo string              `json:"in_response_to"`
	Audience     []string            `json:"audience"`
	NameID       string              `json:"name_id"`
	Attributes   map[string][]string `json:"attributes"`
	Signed       bool                `json:"signed"`
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("usage: casdoor-saml-consumer request|verify [flags]"))
	}
	var err error
	switch os.Args[1] {
	case "request":
		err = makeRequest(os.Args[2:])
	case "verify":
		err = verifyResponse(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatal(err)
	}
}

func serviceProvider(metadataFile, entityID, acsURL string) (*saml.ServiceProvider, error) {
	metadataXML, err := os.ReadFile(metadataFile)
	if err != nil {
		return nil, err
	}
	idpMetadata, err := samlsp.ParseMetadata(metadataXML)
	if err != nil {
		return nil, fmt.Errorf("parse IdP metadata: %w", err)
	}
	metadataURL, err := url.Parse(entityID)
	if err != nil {
		return nil, err
	}
	acs, err := url.Parse(acsURL)
	if err != nil {
		return nil, err
	}
	return &saml.ServiceProvider{
		EntityID:          entityID,
		MetadataURL:       *metadataURL,
		AcsURL:            *acs,
		IDPMetadata:       idpMetadata,
		AuthnNameIDFormat: saml.UnspecifiedNameIDFormat,
	}, nil
}

func makeRequest(args []string) error {
	flags := flag.NewFlagSet("request", flag.ContinueOnError)
	metadataFile := flags.String("metadata", "", "IdP metadata file")
	entityID := flags.String("entity-id", "", "SP entity ID")
	acsURL := flags.String("acs-url", "", "SP assertion consumer URL")
	ssoURL := flags.String("sso-url", "", "IdP SSO URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *metadataFile == "" || *entityID == "" || *acsURL == "" || *ssoURL == "" {
		return errors.New("metadata, entity-id, acs-url and sso-url are required")
	}
	sp, err := serviceProvider(*metadataFile, *entityID, *acsURL)
	if err != nil {
		return err
	}
	request, err := sp.MakeAuthenticationRequest(*ssoURL, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		return fmt.Errorf("make AuthnRequest: %w", err)
	}
	raw, err := xml.Marshal(request)
	if err != nil {
		return err
	}
	var compressed bytes.Buffer
	deflater, err := flate.NewWriter(&compressed, flate.DefaultCompression)
	if err != nil {
		return err
	}
	if _, err := deflater.Write(raw); err != nil {
		return err
	}
	if err := deflater.Close(); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(requestOutput{
		ID: request.ID, Request: base64.StdEncoding.EncodeToString(compressed.Bytes()),
	})
}

func verifyResponse(args []string) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	metadataFile := flags.String("metadata", "", "IdP metadata file")
	entityID := flags.String("entity-id", "", "SP entity ID")
	acsURL := flags.String("acs-url", "", "SP assertion consumer URL")
	responseFile := flags.String("response", "", "base64 SAMLResponse file")
	requestID := flags.String("request-id", "", "matching AuthnRequest ID")
	expectedNameID := flags.String("name-id", "", "expected NameID")
	var expectedAttributes stringList
	flags.Var(&expectedAttributes, "attribute", "required name=value attribute; repeatable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *metadataFile == "" || *entityID == "" || *acsURL == "" || *responseFile == "" || *requestID == "" {
		return errors.New("metadata, entity-id, acs-url, response and request-id are required")
	}
	sp, err := serviceProvider(*metadataFile, *entityID, *acsURL)
	if err != nil {
		return err
	}
	encoded, err := os.ReadFile(*responseFile)
	if err != nil {
		return err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return fmt.Errorf("decode SAMLResponse: %w", err)
	}
	var response saml.Response
	if err := xml.Unmarshal(decoded, &response); err != nil {
		return fmt.Errorf("parse SAMLResponse envelope: %w", err)
	}
	assertion, err := sp.ParseXMLResponse(decoded, []string{*requestID}, sp.AcsURL)
	if err != nil {
		return fmt.Errorf("validate SAMLResponse: %w", err)
	}
	if assertion.Signature == nil {
		return errors.New("SAML assertion is not signed")
	}
	if response.Destination != *acsURL || response.InResponseTo != *requestID {
		return fmt.Errorf("response destination/request mismatch: destination=%q inResponseTo=%q", response.Destination, response.InResponseTo)
	}
	if assertion.Issuer.Value != sp.IDPMetadata.EntityID {
		return fmt.Errorf("assertion issuer %q does not match metadata %q", assertion.Issuer.Value, sp.IDPMetadata.EntityID)
	}
	nameID := ""
	if assertion.Subject != nil && assertion.Subject.NameID != nil {
		nameID = assertion.Subject.NameID.Value
	}
	if *expectedNameID != "" && nameID != *expectedNameID {
		return fmt.Errorf("NameID %q does not match %q", nameID, *expectedNameID)
	}
	attributes := map[string][]string{}
	for _, statement := range assertion.AttributeStatements {
		for _, attribute := range statement.Attributes {
			for _, value := range attribute.Values {
				attributes[attribute.Name] = append(attributes[attribute.Name], value.Value)
			}
		}
	}
	for _, expected := range expectedAttributes {
		name, value, ok := strings.Cut(expected, "=")
		if !ok || name == "" {
			return fmt.Errorf("invalid expected attribute %q", expected)
		}
		if !contains(attributes[name], value) {
			return fmt.Errorf("SAML attribute %s=%q does not contain %q", name, attributes[name], value)
		}
	}
	audiences := []string{}
	if assertion.Conditions != nil {
		for _, restriction := range assertion.Conditions.AudienceRestrictions {
			audiences = append(audiences, restriction.Audience.Value)
		}
	}
	if !contains(audiences, *entityID) {
		return fmt.Errorf("SAML audience does not contain SP entity ID %q", *entityID)
	}
	for name := range attributes {
		sort.Strings(attributes[name])
	}
	sort.Strings(audiences)
	return json.NewEncoder(os.Stdout).Encode(assertionOutput{
		Issuer: assertion.Issuer.Value, Destination: response.Destination,
		InResponseTo: response.InResponseTo, Audience: audiences,
		NameID: nameID, Attributes: attributes, Signed: true,
	})
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
