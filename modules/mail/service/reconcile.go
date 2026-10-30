package main

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

type projectionStore interface {
	MailboxProjection(context.Context, string) (store.Projection, error)
	StageMailbox(context.Context, identity.MailboxResource, string) (store.Projection, error)
	ConfirmMailbox(context.Context, string, string, string) (store.Projection, error)
	ProjectionFailed(context.Context, string, string, string) error
	WithholdMailbox(context.Context, string) error
	DenyPerson(context.Context, string) error
	KnownPersons(context.Context) ([]string, error)
	MailboxReferences(context.Context) ([]identity.MailboxReference, error)
}
type configurationDirectory interface {
	Person(context.Context, string) (identity.Person, error)
	MailboxReferences(context.Context) ([]identity.MailboxReference, error)
	MailboxConfiguration(context.Context, string, string) (identity.MailboxResource, error)
}
type configurationDMS interface {
	Read(context.Context, string) (dms.Receipt, error)
	Apply(context.Context, dms.ApplyRequest) (dms.Receipt, error)
}
type reconciliationError string

func (e reconciliationError) Error() string { return string(e) }

type sourceReconciliationError struct{ cause error }

func (e sourceReconciliationError) Error() string { return "source-routes-unconfirmed" }
func (e sourceReconciliationError) Unwrap() error { return e.cause }

func inconsistentSource(err error) bool {
	return errors.Is(err, store.ErrIngressChanged) || errors.Is(err, ingress.ErrIdentity) || errors.Is(err, ingress.ErrRuleConflict)
}

type mailCoordinator struct {
	store          projectionStore
	directory      configurationDirectory
	dms            configurationDMS
	mu             sync.RWMutex
	lastSuccess    time.Time
	lastError      string
	companyEnabled bool
	sources        interface {
		Synchronize(context.Context, map[string]string) error
	}
}

func (c *mailCoordinator) synchronizeSources(ctx context.Context, ready map[string]string) error {
	if c.sources != nil {
		if e := c.sources.Synchronize(ctx, ready); e != nil {
			return sourceReconciliationError{cause: e}
		}
	}
	return nil
}

func (c *mailCoordinator) failProjection(ctx context.Context, p store.Projection, code string) error {
	if p.Mailbox.ID != "" && !p.Applied {
		if err := c.store.ProjectionFailed(ctx, p.Mailbox.ID, p.Revision, code); err != nil {
			return err
		}
	}
	return reconciliationError(code)
}
func completeResource(r identity.MailboxResource) bool {
	if r.State == "pending" {
		return false
	}
	for _, address := range r.Addresses {
		if address.State == "pending" {
			return false
		}
	}
	return true
}
func (c *mailCoordinator) reconcileMailbox(ctx context.Context, ref identity.MailboxReference) error {
	previous, err := c.store.MailboxProjection(ctx, ref.ID)
	if err != nil && !errors.Is(err, store.ErrProjectionConflict) {
		return err
	}
	resource, err := c.directory.MailboxConfiguration(ctx, ref.OwnerAnchor, ref.ID)
	if err != nil {
		if !errors.Is(err, identity.ErrDirectoryUnavailable) && previous.Mailbox.ID != "" {
			if stop := c.store.WithholdMailbox(ctx, ref.ID); stop != nil {
				return stop
			}
			if stop := c.store.ProjectionFailed(ctx, ref.ID, previous.Revision, "directory-resource-missing"); stop != nil {
				return stop
			}
		}
		return reconciliationError("directory-config-unavailable")
	}
	fingerprint, _, err := store.ProjectionFingerprint(resource)
	if err != nil || resource.ID != ref.ID || resource.OwnerAnchor != ref.OwnerAnchor {
		return reconciliationError("directory-config-invalid")
	}
	readback, readErr := c.dms.Read(ctx, ref.ID)
	if previous.Mailbox.ID != "" && previous.Applied && previous.Fingerprint == fingerprint && previous.Mailbox.State == resource.State {
		if readErr == nil && readback.Applied && readback.MailboxID == ref.ID && readback.Revision == previous.Revision && readback.Fingerprint == fingerprint {
			fresh, err := c.directory.MailboxConfiguration(ctx, ref.OwnerAnchor, ref.ID)
			actual, _, hashErr := store.ProjectionFingerprint(fresh)
			if err == nil && hashErr == nil && actual == fingerprint {
				err = c.synchronizeSources(ctx, nil)
				if inconsistentSource(err) {
					// An applied snapshot is no permission to revive an unknown
					// current source identity/version or changed public rule. Keep
					// receiving configuration/history while closing old grants.
					if stop := c.store.WithholdMailbox(ctx, ref.ID); stop != nil {
						return stop
					}
					if stop := c.store.ProjectionFailed(ctx, ref.ID, previous.Revision, "source-state-unrecognized"); stop != nil {
						return stop
					}
				}
				return err
			}
			if errors.Is(err, identity.ErrDirectoryUnavailable) {
				return reconciliationError("directory-config-unavailable")
			}
			if err = c.store.WithholdMailbox(ctx, ref.ID); err != nil {
				return err
			}
			return reconciliationError("directory-config-changed")
		}
		if err = c.store.WithholdMailbox(ctx, ref.ID); err != nil {
			return err
		}
	}
	projection, err := c.store.StageMailbox(ctx, resource, previous.Revision)
	if err != nil {
		return err
	}
	if !completeResource(resource) {
		if err = c.synchronizeSources(ctx, nil); err != nil {
			return c.failProjection(ctx, projection, "source-withdrawal-unconfirmed")
		}
		return c.failProjection(ctx, projection, "directory-pending")
	}
	expected := ""
	if readback.MailboxID == ref.ID {
		expected = readback.Revision
	}
	// Unknown/corrupt state has no CAS revision. The exact existing generation
	// can still resume idempotently; a different generation cannot overwrite it.
	_, err = c.dms.Apply(ctx, dms.ApplyRequest{ExpectedRevision: expected, Projection: projection})
	if err != nil {
		code := "dms-unavailable"
		if errors.Is(err, dms.ErrConflict) {
			code = "dms-conflict"
		}
		return c.failProjection(ctx, projection, code)
	}
	readback, err = c.dms.Read(ctx, ref.ID)
	if err != nil || !readback.Applied || readback.MailboxID != ref.ID || readback.Revision != projection.Revision || readback.Fingerprint != projection.Fingerprint {
		return c.failProjection(ctx, projection, "dms-unconfirmed")
	}
	fresh, err := c.directory.MailboxConfiguration(ctx, ref.OwnerAnchor, ref.ID)
	if err != nil {
		return c.failProjection(ctx, projection, "directory-config-unavailable")
	}
	actual, _, err := store.ProjectionFingerprint(fresh)
	if err != nil || actual != projection.Fingerprint {
		return c.failProjection(ctx, projection, "directory-config-changed")
	}
	sourceErr := c.synchronizeSources(ctx, map[string]string{ref.ID: projection.Revision})
	var publicPending forwardPublicPending
	if sourceErr != nil && (!errors.As(sourceErr, &publicPending) || !onlyForwardPending(sourceErr)) {
		return c.failProjection(ctx, projection, "source-routes-unconfirmed")
	}
	// Network reconciliation cannot replace the final directory freshness test.
	if c.sources != nil {
		fresh, err = c.directory.MailboxConfiguration(ctx, ref.OwnerAnchor, ref.ID)
		if err != nil {
			return c.failProjection(ctx, projection, "directory-config-unavailable")
		}
		actual, _, err = store.ProjectionFingerprint(fresh)
		if err != nil || actual != projection.Fingerprint {
			return c.failProjection(ctx, projection, "directory-config-changed")
		}
	}
	_, err = c.store.ConfirmMailbox(ctx, ref.ID, projection.Revision, actual)
	if err != nil {
		return err
	}
	return sourceErr
}

func (c *mailCoordinator) reconcile(ctx context.Context) error {
	known, err := c.store.KnownPersons(ctx)
	if err != nil {
		return err
	}
	firstErr := error(nil)
	checked := map[string]bool{}
	check := func(owner string) {
		if checked[owner] {
			return
		}
		checked[owner] = true
		person, err := c.directory.Person(ctx, owner)
		if err == nil && person.Anchor != owner {
			err = identity.ErrDirectoryUnavailable
		}
		if errors.Is(err, identity.ErrAdmission) {
			err = c.store.DenyPerson(ctx, owner)
		}
		if err != nil && firstErr == nil {
			firstErr = reconciliationError("directory-admission-unavailable")
		}
	}
	for _, owner := range known {
		check(owner)
	}
	refs, err := c.directory.MailboxReferences(ctx)
	if err != nil {
		return reconciliationError("directory-resources-unavailable")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		seen[ref.ID] = true
		check(ref.OwnerAnchor)
		if err := c.reconcileMailbox(ctx, ref); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	local, err := c.store.MailboxReferences(ctx)
	if err != nil {
		return err
	}
	for _, ref := range local {
		if !seen[ref.ID] {
			if err := c.store.WithholdMailbox(ctx, ref.ID); err != nil {
				return err
			}
			p, err := c.store.MailboxProjection(ctx, ref.ID)
			if err != nil {
				return err
			}
			if err = c.store.ProjectionFailed(ctx, ref.ID, p.Revision, "directory-resource-missing"); err != nil {
				return err
			}
			if firstErr == nil {
				firstErr = reconciliationError("directory-resource-missing")
			}
		}
	}
	if err = c.synchronizeSources(ctx, nil); err != nil && firstErr == nil {
		firstErr = err
	}
	if c.companyEnabled {
		reader, readerOK := c.directory.(companyDirectoryReader)
		writer, writerOK := c.store.(companyDirectoryWriter)
		if !readerOK || !writerOK {
			return reconciliationError("directory-company-unavailable")
		}
		snapshot, err := reader.CompanySnapshot(ctx)
		if err == nil {
			_, err = writer.SaveCompanyDirectory(ctx, snapshot)
		}
		if err != nil && firstErr == nil {
			firstErr = reconciliationError("directory-company-unavailable")
		}
	}
	return firstErr
}
func (c *mailCoordinator) loop(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		work, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := c.reconcile(work)
		cancel()
		c.recordResult(err)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (c *mailCoordinator) recordResult(err error) {
	c.mu.Lock()
	if err == nil {
		c.lastSuccess = time.Now().UTC()
		c.lastError = ""
	} else {
		c.lastError = "mailbox-reconciliation-incomplete"
	}
	c.mu.Unlock()
}
