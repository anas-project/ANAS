package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

type directoryApplication struct {
	Application string   `json:"application"`
	Groups      []string `json:"groups"`
	Protocol    string   `json:"protocol"`
	CAEP        bool     `json:"caep"`
	AdminGroup  string   `json:"adminGroup"`
}

type directoryLogoutTarget struct {
	Application string `json:"application"`
	SID         string `json:"sid"`
	Subject     string `json:"subject,omitempty"`
	Policy      bool   `json:"policy,omitempty"`
}

type directorySessionSnapshot struct {
	Owner              string                  `json:"owner"`
	UserID             string                  `json:"userId"`
	Name               string                  `json:"name"`
	Subject            string                  `json:"subject"`
	PolicyApplications []string                `json:"policyApplications,omitempty"`
	EventTimestamp     float64                 `json:"eventTimestamp,omitempty"`
	All                bool                    `json:"all"`
	Applications       []string                `json:"applications"`
	Tokens             []string                `json:"tokens"`
	Targets            []directoryLogoutTarget `json:"targets"`
	Sessions           []struct {
		Owner       string   `json:"owner"`
		Name        string   `json:"name"`
		Application string   `json:"application"`
		IDs         []string `json:"sessionId"`
	} `json:"sessions"`
}

type pendingDirectoryLogout struct {
	Snapshot    directorySessionSnapshot `json:"snapshot"`
	Revoked     bool                     `json:"revoked"`
	CreatedAt   int64                    `json:"created_at"`
	NextAttempt int64                    `json:"next_attempt"`
	Attempts    int                      `json:"attempts"`
	LastError   string                   `json:"last_error,omitempty"`
	Unsupported []string                 `json:"unsupported,omitempty"`
}

func managedUserAnchor(user casdoorManagedUser, attribute string) string {
	if value := user.Properties[attribute]; value != "" {
		return value
	}
	return user.ExternalID
}

func filterDirectoryIdentityConflicts(directory []casdoorDirectoryUser, managed []casdoorManagedUser, attribute string) ([]casdoorDirectoryUser, string) {
	result := []casdoorDirectoryUser{}
	conflicts := []string{}
	for _, entry := range directory {
		conflict := false
		for _, user := range managed {
			anchor := managedUserAnchor(user, attribute)
			if strings.EqualFold(user.Name, directoryUsername(entry)) && anchor != "" && anchor != entry.Attributes[attribute] {
				conflict = true
				break
			}
		}
		if conflict {
			conflicts = append(conflicts, directoryUsername(entry))
			continue
		}
		result = append(result, entry)
	}
	if len(conflicts) > 0 {
		return result, "directory identity conflict; admission removed for " + strings.Join(conflicts, ",")
	}
	return result, ""
}

// Reconcile from current LDAP authority. Repeated removal is intentional: a
// token created during the previous update must also be caught by reconciliation.
func planDirectoryRevocations(directory []casdoorDirectoryUser, managed []casdoorManagedUser, memberships map[string][]string, settings directoryWatchSettings) ([]directorySessionSnapshot, error) {
	byAnchor, err := indexDirectoryUsers(directory, settings.identityAnchor)
	if err != nil {
		return nil, err
	}
	result := []directorySessionSnapshot{}
	for _, user := range managed {
		anchor := managedUserAnchor(user, settings.identityAnchor)
		current, exists := byAnchor[anchor]
		all := !exists || anchor == "" || user.Name != directoryUsername(current) || user.ExternalID != anchor
		request := directorySessionSnapshot{Owner: "anas", UserID: user.ID, Name: user.Name, All: all}
		for _, app := range settings.applications {
			allowed := len(app.Groups) == 0
			for _, group := range app.Groups {
				if slices.Contains(memberships[anchor], "anas/"+group) {
					allowed = true
				}
			}
			previouslyAllowed := !user.IsForbidden && !user.IsDeleted && (len(app.Groups) == 0)
			for _, group := range app.Groups {
				if !user.IsForbidden && !user.IsDeleted && slices.Contains(user.Groups, "anas/"+group) {
					previouslyAllowed = true
				}
			}
			roleLost := app.AdminGroup != "" && slices.Contains(user.Groups, "anas/"+app.AdminGroup) && !slices.Contains(memberships[anchor], "anas/"+app.AdminGroup)
			if app.CAEP && ((previouslyAllowed && (!exists || !allowed)) || roleLost) {
				request.PolicyApplications = append(request.PolicyApplications, app.Application)
				request.EventTimestamp = float64(time.Now().UnixMilli()) / 1000
			}
			if all || !allowed || roleLost {
				request.Applications = append(request.Applications, app.Application)
			}
		}
		if all || len(request.Applications) > 0 {
			result = append(result, request)
		}
	}
	return result, nil
}

func (syncer *casdoorLDAPSyncer) revocationRequest(action string, snapshot directorySessionSnapshot, target directoryLogoutTarget) (casdoorAPIResponse, error) {
	body, err := json.Marshal(map[string]any{"owner": "anas", "action": action, "snapshot": snapshot, "target": target})
	if err != nil {
		return casdoorAPIResponse{}, err
	}
	return syncer.request(http.MethodPost, "anas-directory-revocation", "anas/directory", bytes.NewReader(body))
}

func (syncer *casdoorLDAPSyncer) loadPendingLogouts() error {
	if syncer.pendingLoaded {
		return nil
	}
	if syncer.settings.pendingFile == "" {
		syncer.pendingLoaded = true
		return nil
	}
	data, err := os.ReadFile(syncer.settings.pendingFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		if err := json.Unmarshal(data, &syncer.pendingLogouts); err != nil {
			return fmt.Errorf("invalid pending directory logout file: %w", err)
		}
		for _, item := range syncer.pendingLogouts {
			if item.Snapshot.Owner != "anas" || item.Snapshot.UserID == "" || item.CreatedAt <= 0 {
				return fmt.Errorf("invalid pending directory logout record")
			}
		}
	}
	syncer.pendingLoaded = true
	return nil
}

func (syncer *casdoorLDAPSyncer) savePendingLogouts() error {
	if syncer.settings.pendingFile == "" {
		return fmt.Errorf("pending directory logout path is required")
	}
	return writeJSONAtomic(syncer.settings.pendingFile, syncer.pendingLogouts)
}

func (syncer *casdoorLDAPSyncer) prepareLogouts(requests []directorySessionSnapshot) error {
	if err := syncer.loadPendingLogouts(); err != nil {
		return err
	}
	for _, request := range requests {
		response, err := syncer.revocationRequest("prepare", request, directoryLogoutTarget{})
		if err != nil {
			return err
		}
		var snapshot directorySessionSnapshot
		if err := json.Unmarshal(response.Data, &snapshot); err != nil {
			return err
		}
		if len(snapshot.Tokens) == 0 && len(snapshot.Sessions) == 0 && len(snapshot.Targets) == 0 {
			continue
		}
		// Rename and the second capture can contain the same issued session.
		// Keep notification intent once while retaining both provider snapshots.
		targets := snapshot.Targets[:0]
		for _, target := range snapshot.Targets {
			duplicate := false
			for _, saved := range syncer.pendingLogouts {
				if saved.Snapshot.Owner != snapshot.Owner || saved.Snapshot.UserID != snapshot.UserID {
					continue
				}
				if slices.Contains(saved.Snapshot.Targets, target) && (!target.Policy || saved.Snapshot.EventTimestamp == snapshot.EventTimestamp) {
					duplicate = true
					break
				}
			}
			if !duplicate {
				targets = append(targets, target)
			}
		}
		snapshot.Targets = targets
		// A cursor replay can repeat the same captured sessions. Keep one record.
		encoded, _ := json.Marshal(snapshot)
		duplicate := false
		for _, saved := range syncer.pendingLogouts {
			previous, _ := json.Marshal(saved.Snapshot)
			if bytes.Equal(encoded, previous) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		pending := pendingDirectoryLogout{Snapshot: snapshot, CreatedAt: time.Now().Unix()}
		for _, session := range snapshot.Sessions {
			for _, app := range syncer.settings.applications {
				if session.Application == app.Application && app.Protocol == "saml" && !slices.Contains(pending.Unsupported, app.Application) {
					pending.Unsupported = append(pending.Unsupported, app.Application)
				}
			}
		}
		syncer.pendingLogouts = append(syncer.pendingLogouts, pending)
		if err := syncer.savePendingLogouts(); err != nil {
			syncer.pendingLogouts = syncer.pendingLogouts[:len(syncer.pendingLogouts)-1]
			return err
		}
	}
	return nil
}

func (syncer *casdoorLDAPSyncer) revokePreparedLogouts() error {
	for i := range syncer.pendingLogouts {
		item := &syncer.pendingLogouts[i]
		if item.Revoked {
			continue
		}
		if _, err := syncer.revocationRequest("revoke", item.Snapshot, directoryLogoutTarget{}); err != nil {
			return err
		}
		item.Revoked = true
		if err := syncer.savePendingLogouts(); err != nil {
			item.Revoked = false
			return err
		}
	}
	return nil
}

// Delivery has its own retry schedule and never controls the directory cursor.
// Persist every acknowledgment before proceeding; retries address captured sid.
func (syncer *casdoorLDAPSyncer) deliverPendingLogouts(now time.Time) error {
	if err := syncer.loadPendingLogouts(); err != nil {
		return err
	}
	budget := 8
	for i := range syncer.pendingLogouts {
		item := &syncer.pendingLogouts[i]
		if !item.Revoked || item.NextAttempt > now.Unix() {
			continue
		}
		item.LastError = ""
		for len(item.Snapshot.Targets) > 0 && budget > 0 {
			budget--
			target := item.Snapshot.Targets[0]
			if _, err := syncer.revocationRequest("deliver", item.Snapshot, target); err != nil {
				item.LastError = err.Error()
				break
			}
			item.Snapshot.Targets = item.Snapshot.Targets[1:]
			if err := syncer.savePendingLogouts(); err != nil {
				return err
			}
		}
		if len(item.Unsupported) > 0 {
			item.LastError = "SAML session logout is unavailable for " + strings.Join(item.Unsupported, ",")
		}
		if item.LastError != "" {
			item.Attempts++
			delays := []int64{2, 5, 10, 30, 60}
			item.NextAttempt = now.Unix() + delays[min(item.Attempts-1, len(delays)-1)]
		}
	}
	retained := syncer.pendingLogouts[:0]
	for _, item := range syncer.pendingLogouts {
		if !item.Revoked || len(item.Snapshot.Targets) > 0 || len(item.Unsupported) > 0 {
			retained = append(retained, item)
		}
	}
	syncer.pendingLogouts = retained
	if len(retained) > 0 || syncer.settings.pendingFile != "" {
		return syncer.savePendingLogouts()
	}
	return nil
}

func (syncer *casdoorLDAPSyncer) logoutHealth(now time.Time) (int, int64, string) {
	var oldest int64
	var problem string
	for _, item := range syncer.pendingLogouts {
		if oldest == 0 || item.CreatedAt < oldest {
			oldest = item.CreatedAt
		}
		if !item.Revoked || len(item.Unsupported) > 0 || now.Unix()-item.CreatedAt >= 60 {
			problem = item.LastError
			if problem == "" {
				problem = "directory logout is overdue"
			}
		}
	}
	return len(syncer.pendingLogouts), oldest, problem
}
