// TEST_CASES: MAILMOD-T-012
package ingress

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
	"github.com/anas-project/ANAS/modules/mail/store"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func cfFixtureUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		t.Fatal(e)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func cfFixtureDatabase(t *testing.T) (store.Store, context.Context) {
	t.Helper()
	file := os.Getenv("MAIL_STORE_TEST_CONFIG")
	if file == "" {
		t.Skip("isolated real PostgreSQL required; no real-host pass")
	}
	i, e := os.Lstat(file)
	if e != nil || !i.Mode().IsRegular() || i.Mode().Perm()&0077 != 0 {
		t.Fatal("private fixture input required")
	}
	raw, e := os.ReadFile(file)
	var cfg struct {
		DSN string `json:"dsn"`
	}
	if e != nil || json.Unmarshal(raw, &cfg) != nil || cfg.DSN == "" {
		t.Fatal("fixture database input invalid")
	}
	db, e := sql.Open("pgx", cfg.DSN)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	t.Cleanup(cancel)
	var name string
	if db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&name) != nil || name != "anas_mail_model_1025" {
		t.Fatal("refusing non-fixture database")
	}
	data := store.Store{DB: db}
	if e = data.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	return data, ctx
}

// Actual PostgreSQL + production coordinator + actual TLS, with a controlled
// Cloudflare API. Native DMS/AD and actual provider interoperability are separate.
func TestPostgreSQLCloudflareRuleSyncRecoversLostRepliesAndRejectsDrift(t *testing.T) {
	data, ctx := cfFixtureDatabase(t)
	source, generation, owner, box, version := cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t), cfFixtureUUID(t)
	address := "cf" + strings.ReplaceAll(box, "-", "") + "@example.test"
	r := identity.MailboxResource{ID: box, OwnerAnchor: owner, Kind: "primary", DefaultAddress: address, State: "active", Version: version, OperationID: version, OperationState: "complete", Addresses: []identity.AddressResource{{Address: address, Kind: "primary", State: "active", Version: version, OperationID: version, OperationState: "complete"}}}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []string{`DELETE FROM mail.cloudflare_rule_operation WHERE source_id=$1`, `DELETE FROM mail.cloudflare_rule WHERE source_id=$1`, `DELETE FROM mail.ingress_binding WHERE source_id=$1`, `DELETE FROM mail.ingress_route_operation WHERE source_id=$1`, `DELETE FROM mail.ingress_route_state WHERE source_id=$1`} {
			if _, e := data.DB.ExecContext(clean, q, source); e != nil {
				t.Error(e)
			}
		}
		for _, q := range []string{`DELETE FROM mail.mailbox_projection WHERE mailbox_id IN(SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.address WHERE mailbox_id IN(SELECT id FROM mail.mailbox WHERE owner_anchor=$1)`, `DELETE FROM mail.mailbox WHERE owner_anchor=$1`, `DELETE FROM mail.person WHERE anchor=$1`} {
			if _, e := data.DB.ExecContext(clean, q, owner); e != nil {
				t.Error(e)
			}
		}
	})
	p, e := data.StageMailbox(ctx, r, "")
	if e != nil {
		t.Fatal(e)
	}
	routes := []Route{{Address: address, MailboxID: box, OwnerAnchor: owner, ClaimVersion: version}}
	op, e := data.StageSourceRoutes(ctx, source, generation, routes, map[string]string{box: p.Revision})
	if e != nil {
		t.Fatal(e)
	}
	if e = data.IssueSourceRoutes(ctx, op); e != nil {
		t.Fatal(e)
	}
	b := store.IngressRouteBinding{IngressBinding: store.IngressBinding{SourceID: source, Generation: generation, Address: address, MailboxID: box, OwnerAnchor: owner, ClaimVersion: version, FirstSequence: 1}}
	if e = data.ObserveSourceRoutes(ctx, store.IngressRouteResult{SourceID: source, Generation: generation, OperationID: op.ID, Fingerprint: op.Fingerprint, Revision: 1, Sequence: 0, Bindings: []store.IngressRouteBinding{b}}); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	rows := map[string]CloudflareRule{}
	posts, deletes := 0, 0
	loseCreate, loseDelete := true, true
	zone, worker, key := strings.Repeat("1", 32), "anas-mail-isolated-rule", strings.Repeat("k", 40)
	base := "/client/v4/zones/" + zone + "/email/routing/rules"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if req.Header.Get("Authorization") != "Bearer "+key {
			t.Error("wrong API credential role")
			w.WriteHeader(403)
			return
		}
		write := func(value any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": value})
		}
		if req.Method == "GET" && req.URL.Path == base {
			list := []CloudflareRule{}
			for _, r := range rows {
				list = append(list, r)
			}
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": list, "result_info": map[string]int{"page": 1, "per_page": 50, "count": len(list), "total_count": len(list)}})
			return
		}
		if req.Method == "POST" && req.URL.Path == base {
			posts++
			var value CloudflareRule
			if json.NewDecoder(req.Body).Decode(&value) != nil || len(value.Matchers) != 1 {
				w.WriteHeader(400)
				return
			}
			var issued bool
			if data.DB.QueryRowContext(req.Context(), `SELECT EXISTS(SELECT 1 FROM mail.cloudflare_rule_operation WHERE source_id=$1 AND address=$2 AND state='issued' AND issued_at IS NOT NULL)`, source, value.Matchers[0].Value).Scan(&issued) != nil || !issued {
				t.Error("provider write preceded durable issued state")
				w.WriteHeader(500)
				return
			}
			for _, r := range rows {
				if r.Matchers[0].Value == value.Matchers[0].Value {
					w.WriteHeader(409)
					return
				}
			}
			value.ID = fmt.Sprintf("%032x", posts)
			rows[value.ID] = value
			if loseCreate {
				loseCreate = false
				w.WriteHeader(503)
				return
			}
			write(value)
			return
		}
		id := strings.TrimPrefix(req.URL.Path, base+"/")
		value, ok := rows[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if req.Method == "GET" {
			write(value)
			return
		}
		if req.Method == "DELETE" {
			var issued bool
			if data.DB.QueryRowContext(req.Context(), `SELECT EXISTS(SELECT 1 FROM mail.cloudflare_rule_operation WHERE source_id=$1 AND expected_id=$2 AND state IN ('issued','superseded') AND issued_at IS NOT NULL)`, source, id).Scan(&issued) != nil || !issued {
				t.Error("delete preceded durable issued state")
				w.WriteHeader(500)
				return
			}
			deletes++
			delete(rows, id)
			if loseDelete {
				loseDelete = false
				w.WriteHeader(503)
				return
			}
			write(map[string]string{"id": id})
			return
		}
		w.WriteHeader(405)
	}))
	t.Cleanup(server.Close)
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	client, e := NewSourceClient(SourceConfig{ID: source, Generation: generation, Origin: "https://source.example.test", Token: strings.Repeat("a", 64), ControlToken: strings.Repeat("b", 64), Domains: []string{"example.test"}, AdditionalCA: ca})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(client.Close)
	transport := client.http.Transport.(*http.Transport)
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	transport.DialContext = func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != "api.cloudflare.com:443" {
			return nil, errors.New("unexpected fixture target")
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	provider, e := NewCloudflareRules(client, zone, worker, key)
	if e != nil {
		t.Fatal(e)
	}
	syncer := func() *CloudflareRuleSync { return &CloudflareRuleSync{Ledger: data, Client: provider} }
	initialSync := syncer()
	if e = initialSync.Synchronize(ctx, routes); !errors.Is(e, ErrRuleUnknown) {
		t.Fatal("lost create response reported success", e)
	}
	failedAt := initialSync.timeAttempt
	if e = initialSync.Synchronize(ctx, routes); !errors.Is(e, ErrRuleUnknown) || !initialSync.timeAttempt.Equal(failedAt) {
		t.Fatal("failure cache postponed its own retry window", e)
	}
	if e = syncer().Synchronize(ctx, routes); e != nil {
		t.Fatal("new process could not reconcile issued creation", e)
	}
	healthy := syncer()
	if e = healthy.Synchronize(ctx, routes); e != nil {
		t.Fatal(e)
	}
	verifiedAt := healthy.lastSuccess
	if e = healthy.Synchronize(ctx, routes); e != nil || !healthy.lastSuccess.Equal(verifiedAt) {
		t.Fatal("cache hit moved the independent readback deadline", e)
	}
	// A replacement must recognize the old entrance through its actual PG
	// record, without deleting it or publishing a duplicate literal recipient.
	nextCfg := client.cfg
	nextCfg.ID, nextCfg.Generation, nextCfg.Origin = cfFixtureUUID(t), cfFixtureUUID(t), "https://replacement.example.test"
	nextSource, e := NewSourceClient(nextCfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(nextSource.Close)
	nextTransport := nextSource.http.Transport.(*http.Transport)
	nextTransport.TLSClientConfig.ServerName = "127.0.0.1"
	nextTransport.DialContext = transport.DialContext
	nextProvider, e := NewCloudflareRules(nextSource, zone, "anas-mail-isolated-next", key)
	if e != nil {
		t.Fatal(e)
	}
	nextSync := &CloudflareRuleSync{Ledger: data, Client: nextProvider}
	if e = nextSync.Preflight(ctx, routes, []*CloudflareRuleSync{healthy}); e != nil {
		t.Fatal("replacement rejected exact known draining rule", e)
	}
	var open int
	if e = data.DB.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_binding WHERE source_id=$1 AND closed_sequence IS NULL`, source).Scan(&open); e != nil || open != 1 {
		t.Fatal("replacement preflight withdrew the old source", e, open)
	}
	mu.Lock()
	if posts != 1 || len(rows) != 1 {
		t.Fatal("lost create response duplicated provider rule")
	}
	var id string
	for k := range rows {
		id = k
	}
	original := rows[id]
	changed := original
	changed.Enabled = false
	rows[id] = changed
	mu.Unlock()
	if e = nextSync.Preflight(ctx, routes, []*CloudflareRuleSync{healthy}); e == nil {
		t.Fatal("replacement accepted a drifted old provider rule")
	}
	if e = syncer().Synchronize(ctx, routes); e == nil {
		t.Fatal("provider drift silently adopted")
	}
	mu.Lock()
	if posts != 1 || deletes != 0 || rows[id].Enabled {
		t.Fatal("external edit was overwritten")
	}
	rows[id] = original
	mu.Unlock()
	if e = syncer().Synchronize(ctx, routes); e != nil {
		t.Fatal("matching independent readback did not recover", e)
	}
	remove, e := data.StageSourceRoutes(ctx, source, generation, nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = data.IssueSourceRoutes(ctx, remove); e != nil {
		t.Fatal(e)
	}
	fence := int64(0)
	b.ClosedSequence = &fence
	if e = data.ObserveSourceRoutes(ctx, store.IngressRouteResult{SourceID: source, Generation: generation, OperationID: remove.ID, Fingerprint: remove.Fingerprint, Revision: 2, Sequence: 0, Bindings: []store.IngressRouteBinding{b}}); e != nil {
		t.Fatal(e)
	}
	if e = syncer().Synchronize(ctx, nil); !errors.Is(e, ErrRuleUnknown) {
		t.Fatal("lost delete response reported success", e)
	}
	if _, e = data.DB.ExecContext(ctx, `UPDATE mail.cloudflare_rule_operation SET next_attempt=clock_timestamp() WHERE source_id=$1`, source); e != nil {
		t.Fatal(e)
	}
	if e = syncer().Synchronize(ctx, nil); e != nil {
		t.Fatal("lost deletion did not reconcile absence", e)
	}
	mu.Lock()
	count, remaining := deletes, len(rows)
	mu.Unlock()
	if count != 1 || remaining != 0 {
		t.Fatal("delete reply loss repeated mutation or retained public rule")
	}
	p, e = data.MailboxProjection(ctx, box)
	if e != nil || p.Applied {
		t.Fatal("public rule coordinator granted mailbox login", e)
	}
}
