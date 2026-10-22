package application

import "context"

// ApplicationCredentialService reads only explicitly published backend
// credentials from the active deployment. Callers authorize the application ID.
type ApplicationCredentialService interface {
	ReadApplicationCredential(context.Context, string) (ApplicationCredential, error)
}

type ApplicationCredential struct {
	Application string `json:"application"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	URL         string `json:"url"`
}
