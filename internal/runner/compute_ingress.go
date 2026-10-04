package runner

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/anas-project/ANAS/internal/computeingress"
)

// Freeze after calculate, when the capability's provider-owned middleware is
// available, but before rendering/sealing. The frozen grant is what the
// mediator in anasd publishes against; no route is produced here.
func (a *app) prepareComputeIngress(deploymentID string) error {
	var grants []*computeingress.Authorization
	for i := range a.resourceRequests {
		r := &a.resourceRequests[i]
		if r.Contract != "compute" {
			continue
		}
		policy, err := computeingress.ParseSpec(r.Spec)
		if err != nil {
			return fmt.Errorf("resource %s.%s: %w", r.Consumer, r.ID, err)
		}
		if policy == nil {
			r.ComputeIngress = nil
			continue
		}
		grant := &computeingress.Authorization{
			Schema: computeingress.Schema, Deployment: deploymentID,
			Consumer: r.Consumer, Resource: r.ID, Provider: r.Provider, Interface: r.Interface,
			Project: stringSpec(r.Spec, "sandbox"), InstancePrefix: stringSpec(r.Spec, "instance_prefix"),
			LeaseSecretRef: r.LeaseSecretKey, BaseDomain: a.env["BASE_DOMAIN"], Policy: *policy,
		}
		if policy.Auth == "forward_auth" {
			bindings := a.resolvedBindings[r.Consumer]
			provider := bindings[capabilityForwardAuth]
			mod, present := a.reg[provider]
			capability, provides := mod.providedCapability(capabilityForwardAuth)
			if !present || !provides || !contains(capability.Interfaces, interfaceHTTP) || !contains(a.order, provider) || bindings[capabilityForwardAuth+".interface"] != interfaceHTTP {
				return fmt.Errorf("resource %s.%s ingress requires a resolved forward_auth/http capability dependency", r.Consumer, r.ID)
			}
			if a.envOwner["ANAS_FORWARD_AUTH_MIDDLEWARE"] != provider || a.envOwner["ANAS_FORWARD_AUTH_PROVIDER"] != provider || a.env["ANAS_FORWARD_AUTH_PROVIDER"] != provider {
				return fmt.Errorf("resource %s.%s ingress requires middleware output owned by its bound forward_auth provider", r.Consumer, r.ID)
			}
			grant.ForwardAuth = &computeingress.ForwardAuth{Provider: provider, Middleware: a.env["ANAS_FORWARD_AUTH_MIDDLEWARE"]}
		}
		if err := grant.Validate(); err != nil {
			return fmt.Errorf("resource %s.%s: %w", r.Consumer, r.ID, err)
		}
		r.ComputeIngress = grant
		grants = append(grants, grant)
	}
	if len(grants) == 0 {
		return nil
	}
	hosts, err := a.computeIngressReservedHosts()
	if err != nil {
		return err
	}
	return computeingress.ValidateNamespaces(grants, hosts)
}

// Module domain declarations and literal file-provider Host rules are the
// deployment's known reservations. Opaque hostname matchers require an explicit
// reservation adapter; do not pretend to prove disjointness by ignoring them.
func (a *app) computeIngressReservedHosts() ([]string, error) {
	var hosts []string
	for _, name := range a.order {
		if a.reg[name].PublishesDomain {
			host := a.env[strings.ToUpper(strings.ReplaceAll(name, "-", "_"))+"_DOMAIN"]
			if host != "" {
				hosts = append(hosts, strings.ToLower(strings.TrimSuffix(host, ".")))
			}
		}
	}
	for key, rule := range a.env {
		if !strings.HasPrefix(key, "ANAS_TRAEFIK_ROUTE__") || !strings.HasSuffix(key, "__RULE") {
			continue
		}
		// Only the existing literal Host(`name`) form is understood here. A
		// broader rule may claim any name, so it cannot coexist implicitly.
		match := ingressLiteralHost.FindStringSubmatch(rule)
		if len(match) != 2 {
			return nil, fmt.Errorf("compute ingress cannot reserve domains alongside an opaque file-provider route rule")
		}
		hosts = append(hosts, strings.ToLower(match[1]))
	}
	return hosts, nil
}

var ingressLiteralHost = regexp.MustCompile("^Host\\(`([a-zA-Z0-9.-]+)`\\)$")

func validateFrozenComputeIngress(r ResourceRequest, deploymentID string) error {
	if r.Contract != "compute" {
		if r.ComputeIngress != nil {
			return fmt.Errorf("HTTP compute authorization is attached to a non-compute resource")
		}
		return nil
	}
	if err := r.ComputeIngress.ValidateSpec(r.Spec); err != nil {
		return err
	}
	if g := r.ComputeIngress; g != nil {
		if g.Deployment != deploymentID || g.Consumer != r.Consumer || g.Resource != r.ID || g.Provider != r.Provider || g.Interface != r.Interface || g.LeaseSecretRef != r.LeaseSecretKey {
			return fmt.Errorf("frozen HTTP authorization does not match the deployment lease")
		}
	}
	return nil
}
