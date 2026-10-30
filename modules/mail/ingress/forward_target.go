package ingress

import "github.com/anas-project/ANAS/modules/mail/store"

// ForwardTarget derives the independent SMTP destination for one published
// claim. It is a routing identifier, never an authentication credential.
// A new source generation or claim gets a different destination so a stale
// provider rule cannot silently deliver to a later owner of the same address.
func ForwardTarget(source, generation string, route Route, domain string) (string, error) {
	target, err := store.ForwardDestination(source, generation, route, domain)
	if err != nil {
		return "", ErrConfiguration
	}
	return target, nil
}
