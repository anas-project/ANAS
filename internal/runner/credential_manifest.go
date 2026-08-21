package runner

import (
	"fmt"
	"sort"
	"strings"
)

// projectProvidedCredentials publishes one provider's settled value before the
// next Module calculate Hook runs. This is what makes a credential edge a real
// data dependency even when a consumer uses a differently named projection.
func (a *app) projectProvidedCredentials(owner string) error {
	for _, provider := range a.reg[owner].CredentialProviders {
		value := a.env[provider.SecretKey]
		if value == "" {
			return fmt.Errorf("credential %s provider did not materialize %s", provider.ID, provider.SecretKey)
		}
		for _, consumerModule := range a.order {
			for _, consumer := range a.reg[consumerModule].CredentialConsumers {
				if consumer.Credential != provider.ID {
					continue
				}
				if existing := a.env[consumer.Projection]; existing != "" && existing != value {
					return fmt.Errorf("credential %s projection %s conflicts with an existing value", provider.ID, consumer.Projection)
				}
				a.env[consumer.Projection] = value
				a.setEnvOwner(consumer.Projection, owner)
			}
		}
	}
	return nil
}

// prepareDeploymentCredentials resolves static Module declarations against the
// materialized Secret Store and environment. It runs after calculate so a
// provider Hook has already generated any ANAS-owned value, but before either
// the Store or deployment manifest is written.
func (a *app) prepareDeploymentCredentials() error {
	if a.secrets == nil {
		return fmt.Errorf("credential inventory requires the Secret Store")
	}
	consumersByID := map[string][]CredentialConsumer{}
	consumerModulesByID := map[string][]string{}
	for _, name := range a.order {
		for _, consumer := range a.reg[name].CredentialConsumers {
			consumersByID[consumer.Credential] = append(consumersByID[consumer.Credential], consumer)
			consumerModulesByID[consumer.Credential] = append(consumerModulesByID[consumer.Credential], name)
		}
	}

	credentials := []deploymentCredential{}
	seenIDs := map[string]bool{}
	seenKeys := map[string]bool{}
	for _, owner := range a.order {
		for _, provider := range a.reg[owner].CredentialProviders {
			if seenIDs[provider.ID] || seenKeys[provider.SecretKey] {
				return fmt.Errorf("credential inventory contains duplicate provider %s or Secret key %s", provider.ID, provider.SecretKey)
			}
			seenIDs[provider.ID] = true
			seenKeys[provider.SecretKey] = true
			value := a.env[provider.SecretKey]
			stored := a.secrets.values[provider.SecretKey]
			if value == "" || stored == "" || value != stored {
				return fmt.Errorf("credential %s projection is missing or differs from its Secret Store record", provider.ID)
			}
			metadata, ok := a.secrets.metadata[provider.SecretKey]
			if !ok || strings.TrimSpace(metadata.Owner) == "" || strings.TrimSpace(metadata.Kind) == "" || strings.TrimSpace(metadata.Provenance) == "" {
				return fmt.Errorf("credential %s Secret Store metadata is incomplete", provider.ID)
			}
			authority := "external"
			if metadata.Owner == owner && metadata.Kind == "generated" && metadata.Provenance == "module-hook" {
				authority = "anas"
			}
			if metadata.Generation == 0 {
				metadata.Generation = 1
				a.secrets.SetWithMetadata(provider.SecretKey, value, metadata)
			}
			consumerModules := uniqueStrings(consumerModulesByID[provider.ID])
			sort.Strings(consumerModules)
			for _, consumer := range consumersByID[provider.ID] {
				if existing := a.env[consumer.Projection]; existing != "" && existing != value {
					return fmt.Errorf("credential %s projection %s conflicts with an existing value", provider.ID, consumer.Projection)
				}
				a.env[consumer.Projection] = value
				a.setEnvOwner(consumer.Projection, owner)
			}
			projections := credentialValueProjections(a, provider.ID, owner, provider.SecretKey, value)
			credentials = append(credentials, deploymentCredential{
				ID: provider.ID, SecretKey: provider.SecretKey, Owner: owner,
				Consumers: consumerModules, Kind: provider.Kind, Authority: authority,
				RotationMode: provider.RotationMode, Generation: metadata.Generation,
				DesiredProjection: "deployment-secret://" + provider.ID,
				Generator:         provider.Generator, Lifecycle: provider.Lifecycle,
				Controls:    append([]string{}, provider.Controls...),
				Projections: projections,
			})
		}
	}
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].ID < credentials[j].ID })
	a.credentials = credentials
	return nil
}

// credentialValueProjections records only locations that the existing env
// scoping policy already authorizes to receive the value. Equality is safe for
// generated high-entropy values and captures aliases published by a Module
// (for example ANAS_IAM_CLIENT__APP__CLIENT_SECRET) without granting a new
// cross-Module read path.
func credentialValueProjections(a *app, credentialID, owner, secretKey, value string) []deploymentCredentialProjection {
	seen := map[string]bool{}
	projections := []deploymentCredentialProjection{}
	for _, module := range a.order {
		for key, projected := range a.scopedEnv(module) {
			if projected != value {
				continue
			}
			identity := module + "\x00" + key
			if seen[identity] {
				continue
			}
			seen[identity] = true
			projections = append(projections, deploymentCredentialProjection{Module: module, EnvKey: key})
		}
	}
	ownerProjection := owner + "\x00" + secretKey
	if !seen[ownerProjection] {
		projections = append(projections, deploymentCredentialProjection{Module: owner, EnvKey: secretKey})
		seen[ownerProjection] = true
	}
	for _, module := range a.order {
		for _, consumer := range a.reg[module].CredentialConsumers {
			if consumer.Credential != credentialID {
				continue
			}
			identity := module + "\x00" + consumer.Projection
			if !seen[identity] {
				projections = append(projections, deploymentCredentialProjection{Module: module, EnvKey: consumer.Projection})
				seen[identity] = true
			}
		}
	}
	sort.Slice(projections, func(i, j int) bool {
		if projections[i].Module == projections[j].Module {
			return projections[i].EnvKey < projections[j].EnvKey
		}
		return projections[i].Module < projections[j].Module
	})
	return projections
}

func cloneCredentialProviders(in []CredentialProvider) []CredentialProvider {
	out := make([]CredentialProvider, len(in))
	for index, provider := range in {
		out[index] = provider
		out[index].Controls = append([]string{}, provider.Controls...)
	}
	return out
}

func cloneCredentialConsumers(in []CredentialConsumer) []CredentialConsumer {
	return append([]CredentialConsumer{}, in...)
}
