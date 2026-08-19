// Package topologyschema owns the vocabulary shared by configuration parsing
// and topology/capability resolution. It deliberately has no dependency on
// either package so both layers validate the same protocol identifiers without
// creating an import cycle.
package topologyschema

const (
	IAMProtocolOIDC = "oidc"
	IAMProtocolSAML = "saml"
)

// IAMProtocols returns the Core IAM protocol vocabulary in preference-neutral
// order. Callers receive a copy so a manifest or schema cannot mutate the
// shared definition.
func IAMProtocols() []string {
	return []string{IAMProtocolOIDC, IAMProtocolSAML}
}

// IAMLoginProtocols adds the consumer-side automatic selector to the protocol
// vocabulary used by deployment defaults and capability matching.
func IAMLoginProtocols() []string {
	return append([]string{"auto"}, IAMProtocols()...)
}
