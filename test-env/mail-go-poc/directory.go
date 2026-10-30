// TEST_CASES: MAILPOC-T-004, MAILPOC-T-005
// Isolated directory/session feasibility adapter, not a production synchronizer.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"
)

type Contact struct {
	ID         string   `json:"ID"`
	Name       string   `json:"name"`
	Email      []string `json:"email"`
	Department string   `json:"department"`
	Phone      string   `json:"phone:work"`
	Groups     []string `json:"groups"`
}
type DirectoryGroup struct {
	ID   string `json:"ID"`
	Name string `json:"name"`
}

func initDirectorySchema() {
	exec(`ALTER TABLE people ADD COLUMN IF NOT EXISTS allowed boolean NOT NULL DEFAULT false;
 ALTER TABLE people DROP CONSTRAINT IF EXISTS people_uid_key;
 ALTER TABLE sessions ADD COLUMN IF NOT EXISTS oidc_sid text NOT NULL DEFAULT '';
 CREATE TABLE IF NOT EXISTS directory_state(id int PRIMARY KEY CHECK(id=1),cursor bigint NOT NULL DEFAULT 0,checked timestamptz);
 INSERT INTO directory_state(id) VALUES(1) ON CONFLICT DO NOTHING;
 CREATE TABLE IF NOT EXISTS directory_contacts(id text PRIMARY KEY,anchor text UNIQUE NOT NULL,body jsonb NOT NULL);
 CREATE TABLE IF NOT EXISTS directory_groups(id text PRIMARY KEY,anchor text UNIQUE NOT NULL,body jsonb NOT NULL);
 CREATE TABLE IF NOT EXISTS pending_kicks(sid text PRIMARY KEY,created timestamptz NOT NULL DEFAULT now());
 CREATE TABLE IF NOT EXISTS logout_seen(jti text PRIMARY KEY,created timestamptz NOT NULL DEFAULT now());`)
}

// Persist targets and revoke before performing the external action. Each target
// is an opaque session reference, never a mailbox address or shell expression.
func revokeSessions(where string, args ...any) {
	tx, e := db.Begin(ctx)
	must(e)
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, "INSERT INTO pending_kicks(sid) SELECT id FROM sessions WHERE "+where+" ON CONFLICT DO NOTHING", args...)
	must(e)
	_, e = tx.Exec(ctx, "DELETE FROM sessions WHERE "+where, args...)
	must(e)
	must(tx.Commit(ctx))
}

func syncDirectory() {
	c := directory()
	defer c.Close()
	c.SetTimeout(8 * time.Second)
	base := "CN=Users,DC=mailtest,DC=invalid"
	users, e := c.SearchWithPaging(ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 8, false,
		"(&(objectClass=user)(anasIdentityAnchor=*))", []string{"anasIdentityAnchor", "sAMAccountName", "displayName", "cn", "mail", "department", "telephoneNumber", "userAccountControl"}, nil), 200)
	must(e)
	groups, e := c.SearchWithPaging(ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 8, false,
		"(&(objectClass=group)(anasIdentityAnchor=*))", []string{"anasIdentityAnchor", "cn"}, nil), 200)
	must(e)
	// Read every authoritative LDAP result before beginning a replacing transaction.
	groupIDs := map[string]string{}
	groupBodies := map[string]DirectoryGroup{}
	for _, g := range groups.Entries {
		a := g.GetAttributeValue("anasIdentityAnchor")
		id := "g-" + hash(a)[:24]
		groupIDs[strings.ToLower(g.DN)] = id
		groupBodies[a] = DirectoryGroup{id, g.GetAttributeValue("cn")}
	}
	type Person struct {
		Anchor, UID string
		Allowed     bool
		Contact     Contact
	}
	people := []Person{}
	for _, u := range users.Entries {
		a, uid := u.GetAttributeValue("anasIdentityAnchor"), u.GetAttributeValue("sAMAccountName")
		membership, e := c.Search(ldap.NewSearchRequest(base, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 8, false,
			"(&(objectClass=group)(member:1.2.840.113556.1.4.1941:="+ldap.EscapeFilter(u.DN)+"))", []string{"cn"}, nil))
		must(e)
		admitted := false
		ids := []string{}
		for _, g := range membership.Entries {
			admitted = admitted || g.GetAttributeValue("cn") == "MailUsers"
			if id := groupIDs[strings.ToLower(g.DN)]; id != "" {
				ids = append(ids, id)
			}
		}
		flags, e := strconv.Atoi(u.GetAttributeValue("userAccountControl"))
		must(e)
		name := u.GetAttributeValue("displayName")
		if name == "" {
			name = u.GetAttributeValue("cn")
		}
		contact := Contact{"p-" + hash(a)[:24], name, []string{u.GetAttributeValue("mail")}, u.GetAttributeValue("department"), u.GetAttributeValue("telephoneNumber"), ids}
		people = append(people, Person{a, uid, admitted && flags&2 == 0, contact})
	}
	tx, e := db.Begin(ctx)
	must(e)
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, "DELETE FROM directory_contacts; DELETE FROM directory_groups; UPDATE people SET allowed=false")
	must(e)
	for a, g := range groupBodies {
		body, e := json.Marshal(g)
		must(e)
		_, e = tx.Exec(ctx, "INSERT INTO directory_groups VALUES($1,$2,$3)", g.ID, a, body)
		must(e)
	}
	for _, p := range people {
		_, e = tx.Exec(ctx, "INSERT INTO people(anchor,uid,allowed) VALUES($1,$2,$3) ON CONFLICT(anchor) DO UPDATE SET uid=excluded.uid,allowed=excluded.allowed", p.Anchor, p.UID, p.Allowed)
		must(e)
		if p.Allowed && p.Contact.Email[0] != "" {
			body, e := json.Marshal(p.Contact)
			must(e)
			_, e = tx.Exec(ctx, "INSERT INTO directory_contacts VALUES($1,$2,$3)", p.Contact.ID, p.Anchor, body)
			must(e)
		}
	}
	_, e = tx.Exec(ctx, `INSERT INTO pending_kicks(sid) SELECT s.id FROM sessions s JOIN people p ON p.anchor=s.owner WHERE NOT p.allowed ON CONFLICT DO NOTHING;
 DELETE FROM sessions WHERE owner IN (SELECT anchor FROM people WHERE NOT allowed);
 UPDATE directory_state SET checked=now() WHERE id=1`)
	must(e)
	must(tx.Commit(ctx))
}

// The source retains current + one rotated file. Partial tails are never parsed.
func journalState() (int64, bool) {
	var cursor int64
	must(db.QueryRow(ctx, "SELECT cursor FROM directory_state WHERE id=1").Scan(&cursor))
	rows := map[int64]string{}
	for _, p := range []string{"/events/events.jsonl.1", "/events/events.jsonl"} {
		f, e := os.Open(p)
		if os.IsNotExist(e) {
			continue
		}
		must(e)
		r := bufio.NewReader(f)
		for {
			line, e := r.ReadString('\n')
			if e == io.EOF {
				break
			}
			must(e)
			var v struct {
				Seq int64  `json:"seq"`
				Op  string `json:"op"`
			}
			must(json.Unmarshal([]byte(line), &v))
			if v.Seq < 1 {
				panic("invalid journal sequence")
			}
			if old, ok := rows[v.Seq]; ok && old != line {
				panic("conflicting journal sequence")
			}
			rows[v.Seq] = line
		}
		f.Close()
	}
	var max int64
	for seq := range rows {
		if seq > max {
			max = seq
		}
	}
	gap := max < cursor
	for n := cursor + 1; n <= max; n++ {
		if _, ok := rows[n]; !ok {
			gap = true
			break
		}
	}
	return max, gap
}

func kickPending() {
	rows, e := db.Query(ctx, "SELECT sid FROM pending_kicks ORDER BY created")
	must(e)
	ids := []string{}
	for rows.Next() {
		var sid string
		must(rows.Scan(&sid))
		ids = append(ids, sid)
	}
	rows.Close()
	client := &http.Client{Timeout: 4 * time.Second}
	for _, sid := range ids {
		body, _ := json.Marshal(map[string]string{"sid": sid})
		r, e := http.NewRequest("POST", "http://mail:8008/kick", bytes.NewReader(body))
		must(e)
		r.SetBasicAuth("kick", secret["kick"])
		response, e := client.Do(r)
		if e != nil {
			continue
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode == 204 {
			exec("DELETE FROM pending_kicks WHERE sid=$1", sid)
		}
	}
}

func directoryTick() {
	defer func() {
		if e := recover(); e != nil {
			log.Printf("directory retry pending (%T)", e)
		}
	}()
	var cursor int64
	var checked time.Time
	must(db.QueryRow(ctx, "SELECT cursor,checked FROM directory_state WHERE id=1").Scan(&cursor, &checked))
	max, gap := journalState()
	if gap || max != cursor || time.Since(checked) > 300*time.Second {
		mutation.Lock()
		func() { defer mutation.Unlock(); syncDirectory() }()
		// This cursor covers authoritative refresh. Kicks have independent durable ACKs.
		exec("UPDATE directory_state SET cursor=$1 WHERE id=1", max)
		log.Printf("directory reconciled seq=%d gap=%t", max, gap)
	}
	kickPending()
}
func directoryLoop() {
	for {
		directoryTick()
		time.Sleep(time.Second)
	}
}

func directoryAPI(w http.ResponseWriter, r *http.Request) {
	if !service(r, "roundcube") || r.Method != "POST" {
		deny(w)
		return
	}
	var owner string
	e := db.QueryRow(ctx, `SELECT s.owner FROM sessions s JOIN people p ON p.anchor=s.owner WHERE s.id=$1 AND s.expires>now() AND p.allowed`, r.FormValue("session_ref")).Scan(&owner)
	if e != nil {
		deny(w)
		return
	}
	if r.URL.Path == "/internal/session" {
		reply(w, map[string]any{"active": true})
		return
	}
	if r.URL.Path == "/internal/addressbooks" {
		reply(w, map[string]any{"sources": []map[string]any{{"id": "company", "name": "Company directory", "readonly": true, "groups": true, "kind": "directory"}}})
		return
	}
	if r.URL.Path != "/internal/addressbooks/query" || r.FormValue("book") != "company" {
		http.NotFound(w, r)
		return
	}
	var result []Contact
	result = []Contact{}
	rows, e := db.Query(ctx, "SELECT body FROM directory_contacts ORDER BY body->>'name'")
	must(e)
	q, id, gid := strings.ToLower(r.FormValue("q")), r.FormValue("id"), r.FormValue("group")
	for rows.Next() {
		var raw []byte
		must(rows.Scan(&raw))
		var p Contact
		must(json.Unmarshal(raw, &p))
		if id != "" && p.ID != id {
			continue
		}
		if gid != "" {
			found := false
			for _, g := range p.Groups {
				found = found || g == gid
			}
			if !found {
				continue
			}
		}
		if q != "" && !strings.Contains(strings.ToLower(p.Name+" "+strings.Join(p.Email, " ")+" "+p.Department+" "+p.Phone), q) {
			continue
		}
		result = append(result, p)
	}
	rows.Close()
	groups := []DirectoryGroup{}
	rows, e = db.Query(ctx, "SELECT body FROM directory_groups ORDER BY body->>'name'")
	must(e)
	for rows.Next() {
		var raw []byte
		must(rows.Scan(&raw))
		var g DirectoryGroup
		must(json.Unmarshal(raw, &g))
		groups = append(groups, g)
	}
	rows.Close()
	reply(w, map[string]any{"contacts": result, "groups": groups})
}

func backchannel(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		deny(w)
		return
	}
	t, e := verifier.VerifyLogout(r.Context(), r.FormValue("logout_token"))
	if e != nil || t.IssuedAt.Before(time.Now().Add(-2*time.Minute)) || t.IssuedAt.After(time.Now().Add(30*time.Second)) {
		deny(w)
		return
	}
	mutation.Lock()
	defer mutation.Unlock()
	var seen bool
	must(db.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM logout_seen WHERE jti=$1)", t.TokenID).Scan(&seen))
	if !seen {
		if t.SessionID != "" {
			revokeSessions("oidc_sid=$1 AND ($2='' OR owner=$2)", t.SessionID, t.Subject)
		} else {
			revokeSessions("owner=$1", t.Subject)
		}
		exec("INSERT INTO logout_seen(jti) VALUES($1) ON CONFLICT DO NOTHING", t.TokenID)
	}
	fmt.Fprint(w, "Logged out")
}
