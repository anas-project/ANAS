package main

import (
	"net/http"

	"github.com/anas-project/ANAS/internal/directorymail"
)

type creationMailReader interface {
	CreationMailConflict(string) (directorymail.AddressConflict, error)
}

func (d *LDAPDirectory) CreationMailConflict(address string) (directorymail.AddressConflict, error) {
	conn, err := d.read()
	if err != nil {
		return directorymail.AddressConflict{}, err
	}
	defer conn.Close()
	resources, err := directorymail.NewPrimaryDirectory(conn, d.config.BaseDN)
	if err != nil {
		return directorymail.AddressConflict{}, err
	}
	return resources.InspectAddress(address)
}

// This read only helps an administrator resolve a conflict. It never reserves
// or releases an address and cannot replace createWithMail's final election.
func (s *Server) creationMailConflict(w http.ResponseWriter, r *http.Request) {
	_, actor, account, ok := s.current(w, r)
	if !ok {
		return
	}
	reader, supported := s.directory.(creationMailReader)
	if !supported || !s.administrator(actor) || !s.creationEnabled() || s.config.Mail == nil {
		failure(w, 403, "access_denied")
		return
	}
	query := r.URL.Query()
	username := query.Get("username")
	if len(query["username"]) != 1 || !newUsername.MatchString(username) {
		failure(w, 400, "invalid_request")
		return
	}
	address, err := directorymail.Address(username, s.config.MailDomain)
	if err != nil {
		failure(w, 400, "invalid_request")
		return
	}
	fact, err := reader.CreationMailConflict(address)
	if err != nil || fact.Address != address {
		failure(w, 503, "mail_management_unavailable")
		return
	}
	result := map[string]any{"address": address, "occupied": fact.Occupied, "kind": fact.Kind, "state": fact.State, "can_maintain": false}
	if fact.Owner != "" {
		person, err := s.directory.Person(fact.Owner)
		if err == nil && s.managedPerson(person) {
			result["owner"] = map[string]string{"anchor": person.Anchor, "username": person.Username, "display_name": person.DisplayName}
			protected := false
			for _, address := range s.config.Mail.Policy.Protected {
				protected = protected || address == fact.Address
			}
			if !protected && !person.Protected && !has(s.config.ProtectedUsers, person.Anchor) && fact.State == "active" && (fact.Kind == "alias" || fact.Kind == "additional") {
				if entry := s.mailManagementURL(actor, account); entry != "" {
					result["management_url"] = entry + "/addresses?owner=" + person.Anchor
				}
			}

		}
	}
	respond(w, 200, result)
}
