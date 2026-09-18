package computeingressruntime

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/computeingress"
	"github.com/anas-project/ANAS/internal/deployment"
)

const fixtureRegistrySchema = "anas.compute-http-fixture-registry/v1"

type fixtureRegistry struct {
	Schema   string                                `json:"schema"`
	Scope    *deployment.HTTPAuthorizationSnapshot `json:"scope"`
	Fixtures []FixtureHTTPExpectation              `json:"fixtures"`
}

// Fixture response identity survives a new Planner session, but no other
// change. In particular restart of the guest changes Incarnation. A registration
// is never an executor receipt and cannot revive a retired reservation.
func fixtureBinding(target PublicationTarget) PublicationTarget {
	target.Publication.Reservation = ""
	return target
}

// PrepareFixtureResponse generates bytes for an administrator to install into
// the selected disposable guest. It neither contacts the guest nor registers a
// publication. Expectations are made before any response is fetched from it.
func PrepareFixtureResponse(target PublicationTarget, path string) (FixtureHTTPExpectation, []byte, error) {
	if err := validateTarget(target.Epoch, target); err != nil {
		return FixtureHTTPExpectation{}, nil, err
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return FixtureHTTPExpectation{}, nil, fmt.Errorf("cannot prepare distinct HTTP fixture response")
	}
	identity, err := json.Marshal(fixtureBinding(target))
	if err != nil {
		return FixtureHTTPExpectation{}, nil, fmt.Errorf("cannot encode HTTP fixture binding")
	}
	body := []byte(fmt.Sprintf("anas-incus-http-fixture/v1\n%x\n%s\n", sha256.Sum256(identity), base64.StdEncoding.EncodeToString(random[:])))
	expected := FixtureHTTPExpectation{Target: target, Path: path, BodyBytes: len(body), BodySHA256: fmt.Sprintf("%x", sha256.Sum256(body))}
	if err := validateFixtureResponse(expected); err != nil {
		return FixtureHTTPExpectation{}, nil, err
	}
	return expected, body, nil
}

// RegisterHTTPFixtures publishes a new private expectation file, not a route or
// an authorization grant. Call from the trusted preparation session while its
// source/journal are valid. Production consumers cannot call this or access the
// file. The administrator installs the pre-generated fixture bytes separately.
func RegisterHTTPFixtures(ctx context.Context, workspace, destination string, fixtures []FixtureHTTPExpectation, authority AuthorizationSource, observer Observer) error {
	if authority == nil || observer == nil || len(fixtures) == 0 || len(fixtures) > 1024 {
		return fmt.Errorf("HTTP fixture registration requires current authority, facts and bounded expectations")
	}
	registrationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	snapshot, err := deployment.NewReader(workspace).HTTPAuthorizations(registrationCtx)
	if err != nil {
		return err
	}
	approved := slices.Clone(fixtures)
	registry := fixtureRegistry{Schema: fixtureRegistrySchema, Scope: snapshot, Fixtures: slices.Clone(fixtures)}
	for i := range registry.Fixtures {
		if validateTarget(snapshot.Epoch, approved[i].Target) != nil {
			return fmt.Errorf("HTTP fixture has no current complete publication target")
		}
		registry.Fixtures[i].Target = fixtureBinding(approved[i].Target)
	}
	sort.Slice(registry.Fixtures, func(i, j int) bool {
		return registry.Fixtures[i].Target.Publication.Host < registry.Fixtures[j].Target.Publication.Host
	})
	body, err := json.Marshal(registry)
	if err != nil || len(body) > privateArtifactLimit {
		return fmt.Errorf("HTTP fixture registry exceeds 1 MiB")
	}
	if _, err := decodeFixtureRegistry(body, snapshot); err != nil {
		return err
	}
	check := func(ctx context.Context) error {
		if err := StillCurrent(ctx, workspace, snapshot); err != nil {
			return err
		}
		for _, fixture := range approved {
			if authority.ValidateAuthorization(ctx, fixture.Target) != nil || observer.ValidateTarget(ctx, fixture.Target) != nil || authority.ValidateAuthorization(ctx, fixture.Target) != nil {
				return fmt.Errorf("HTTP fixture request or instance changed during registration")
			}
		}
		return StillCurrent(ctx, workspace, snapshot)
	}
	if err := check(registrationCtx); err != nil {
		return err
	}
	return publishPrivateArtifact(registrationCtx, destination, body, check)
}

func decodeFixtureRegistry(body []byte, snapshot *deployment.HTTPAuthorizationSnapshot) (fixtureRegistry, error) {
	fail := func() (fixtureRegistry, error) {
		return fixtureRegistry{}, fmt.Errorf("HTTP fixture registry does not match its active bounded schema")
	}
	if len(body) == 0 || len(body) > privateArtifactLimit || snapshot == nil || !validEpoch(snapshot.Epoch) || len(snapshot.Authorizations) == 0 || len(snapshot.Authorizations) > 1024 {
		return fail()
	}
	var registry fixtureRegistry
	if decodeObservedJSON(body, &registry) != nil || registry.Schema != fixtureRegistrySchema || !reflect.DeepEqual(registry.Scope, snapshot) || len(registry.Fixtures) == 0 || len(registry.Fixtures) > 1024 {
		return fail()
	}
	canonical, err := json.Marshal(registry)
	if err != nil || !bytes.Equal(canonical, body) || computeingress.ValidateNamespaces(snapshot.Authorizations, nil) != nil {
		return fail()
	}
	grants := make(map[computeingress.Lease]*computeingress.Authorization)
	for _, grant := range snapshot.Authorizations {
		grants[computeingress.Lease{Consumer: grant.Consumer, Resource: grant.Resource}] = grant
	}
	hosts, responses := make(map[string]bool), make(map[string]bool)
	ports := make(map[instancePort]bool)
	lastHost := ""
	for _, fixture := range registry.Fixtures {
		target, p := fixture.Target, fixture.Target.Publication
		if p.Reservation != "" || validateTargetIdentity(snapshot.Epoch, target) != nil || validateFixtureResponse(fixture) != nil || p.Deployment != snapshot.Deployment || p.Host <= lastHost || hosts[p.Host] || responses[fixture.BodySHA256] || ports[targetPort(target)] {
			return fail()
		}
		grant := grants[p.Lease]
		if grant == nil || grant.BaseDomain != "example.test" || !strings.HasPrefix(p.InstanceID, grant.InstancePrefix) || len(p.InstanceID) <= len(grant.InstancePrefix) || !slices.Contains(grant.Policy.AllowedPorts, p.GuestPort) || grant.Policy.Auth != p.Auth {
			return fail()
		}
		middleware := ""
		if grant.ForwardAuth != nil {
			middleware = grant.ForwardAuth.Middleware
		}
		if middleware != p.Middleware {
			return fail()
		}
		hosts[p.Host], responses[fixture.BodySHA256], ports[targetPort(target)] = true, true, true
		lastHost = p.Host
	}
	return registry, nil
}

// NewRegisteredFixtureHTTPProbe pins a private registration artifact. For each
// request it matches all stable fields, then fills only the *current* Planner
// token. ProbeHTTP still checks current authority and independent facts before
// and after the exchange. The file alone cannot authorize or retain a route.
func NewRegisteredFixtureHTTPProbe(ctx context.Context, workspace, path string, identity TraefikProbeIdentity, authority AuthorizationSource, observer Observer) (*FixtureHTTPProbe, error) {
	if validateProbeIdentity(identity) != nil || authority == nil || observer == nil {
		return nil, fmt.Errorf("registered HTTP probe requires installed identity and independent authorization")
	}
	snapshot, err := deployment.NewReader(workspace).HTTPAuthorizations(ctx)
	if err != nil {
		return nil, err
	}
	body, parent, file, err := readPrivateArtifact(ctx, path)
	if err != nil {
		return nil, err
	}
	registry, err := decodeFixtureRegistry(body, snapshot)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(body)
	registered := make(map[PublicationTarget]FixtureHTTPExpectation)
	for _, fixture := range registry.Fixtures {
		registered[fixture.Target] = fixture
	}
	resolve := func(ctx context.Context, target PublicationTarget) (FixtureHTTPExpectation, error) {
		if validateTarget(snapshot.Epoch, target) != nil {
			return FixtureHTTPExpectation{}, fmt.Errorf("HTTP fixture target is outside the registered epoch")
		}
		if err := StillCurrent(ctx, workspace, snapshot); err != nil {
			return FixtureHTTPExpectation{}, err
		}
		current, currentParent, currentFile, err := readPrivateArtifact(ctx, path)
		if err != nil || !os.SameFile(parent, currentParent) || !os.SameFile(file, currentFile) || sha256.Sum256(current) != digest {
			return FixtureHTTPExpectation{}, fmt.Errorf("HTTP fixture registration changed or became unavailable")
		}
		fixture, ok := registered[fixtureBinding(target)]
		if !ok {
			return FixtureHTTPExpectation{}, fmt.Errorf("HTTP target has no matching registered fixture identity")
		}
		fixture.Target = target
		if err := StillCurrent(ctx, workspace, snapshot); err != nil {
			return FixtureHTTPExpectation{}, err
		}
		return fixture, nil
	}
	if err := StillCurrent(ctx, workspace, snapshot); err != nil {
		return nil, err
	}
	return &FixtureHTTPProbe{identity: identity, authority: authority, observer: observer, expectation: resolve}, nil
}
