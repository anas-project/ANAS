package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type ingressReport struct {
	ID         string                `json:"id"`
	Provider   string                `json:"provider"`
	Mode       string                `json:"mode"`
	Domains    []string              `json:"domains"`
	Local      *store.IngressStatus  `json:"local"`
	LocalCheck string                `json:"local_check"`
	Cloud      *ingress.SourceStatus `json:"cloud"`
	CloudCheck string                `json:"cloud_check"`
	RuleRead   string                `json:"rule_read"`
	CheckedAt  time.Time             `json:"checked_at"`
}

type publicRuleReader interface {
	List(context.Context) ([]ingress.CloudflareRule, error)
}

// This observes read access only. It neither checks write authorization nor
// proves that any particular recipient is routed to the intended mailbox.
func observePublicRuleRead(ctx context.Context, reader publicRuleReader) string {
	if reader == nil {
		return "not-configured"
	}
	_, err := reader.List(ctx)
	if err == nil {
		return "observed"
	}
	if errors.Is(err, ingress.ErrAuthentication) {
		return "authentication-failed"
	}
	return "unavailable"
}

func observeForwardRuleRead(ctx context.Context, reader *ingress.CloudflareForwardRules) string {
	if reader == nil {
		return "not-configured"
	}
	_, err := reader.Inspect(ctx, nil)
	if err == nil {
		return "observed"
	}
	if errors.Is(err, ingress.ErrAuthentication) {
		return "authentication-failed"
	}
	return "unavailable"
}

type ingressInspector interface {
	Inspect(context.Context) []ingressReport
}

func (r *ingressRuntime) Inspect(ctx context.Context) []ingressReport {
	if r == nil {
		return []ingressReport{}
	}
	reports := make([]ingressReport, len(r.entries))
	var wg sync.WaitGroup
	index := 0
	for _, entry := range r.entries {
		i, entry := index, entry
		index++
		wg.Add(1)
		go func() {
			defer wg.Done()
			work, cancel := context.WithTimeout(ctx, 4*time.Second)
			defer cancel()
			v := ingressReport{ID: entry.spec.ID, Provider: entry.spec.Provider, Mode: entry.spec.Mode, Domains: entry.spec.Domains, LocalCheck: "unavailable", CloudCheck: "unavailable", RuleRead: "not-configured", CheckedAt: time.Now().UTC()}
			local, e := r.store.IngressSummary(work, entry.spec.ID, entry.spec.Generation)
			if e == nil {
				v.Local = &local
				v.LocalCheck = "observed"
			}
			cloud, e := entry.client.Status(work)
			if e == nil {
				v.Cloud = &cloud
				v.CloudCheck = "observed"
				expectedNotification := "unconfigured"
				if entry.spec.Notifications {
					expectedNotification = "configured"
				}
				if cloud.CapacityBytes != entry.spec.CapacityBytes || cloud.DeliveredDays != entry.spec.DeliveredDays || cloud.Notifications != expectedNotification {
					v.CloudCheck = "configuration-changed"
				}
				if entry.spec.Provider == "cloud-relay" {
					expectedDomain := ""
					if entry.spec.CloudflareForward != nil {
						expectedDomain = entry.spec.CloudflareForward.Domain
					}
					if cloud.ForwardDomain != expectedDomain {
						v.CloudCheck = "configuration-changed"
					}
				}
			} else if errors.Is(e, ingress.ErrAuthentication) {
				v.CloudCheck = "authentication-failed"
			} else if errors.Is(e, ingress.ErrIdentity) {
				v.CloudCheck = "identity-changed"
			}
			if entry.public != nil && entry.public.Client != nil {
				v.RuleRead = observePublicRuleRead(work, entry.public.Client)
			}
			if entry.forwardReader != nil {
				v.RuleRead = observeForwardRuleRead(work, entry.forwardReader)
			}
			reports[i] = v
		}()
	}
	wg.Wait()
	sort.Slice(reports, func(i, j int) bool { return reports[i].ID < reports[j].ID })
	return reports
}
func (s *loginHandler) ingressStatus(w http.ResponseWriter, r *http.Request) {
	if r.URL.RawQuery != "" || r.URL.ForceQuery {
		loginError(w, 400, "invalid-request")
		return
	}
	person, group, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	result := []ingressReport{}
	if s.sources != nil {
		result = s.sources.Inspect(r.Context())
	}
	fresh, freshGroup, ok := s.setupAccess(w, r)
	if !ok {
		return
	}
	if fresh.Anchor != person.Anchor || freshGroup != group {
		loginError(w, 403, "administrator-required")
		return
	}
	reply(w, 200, map[string]any{"sources": result, "public_ready": false})
}
