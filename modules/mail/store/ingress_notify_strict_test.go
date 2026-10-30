// TEST_CASES: MAILMOD-T-077
package store_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anas-project/ANAS/modules/mail/ingress"
	"github.com/anas-project/ANAS/modules/mail/store"
)

// Uses an explicitly configured isolated PostgreSQL fixture; unlike the
// in-memory unit test it confirms which requests actually reach durable state.
func TestPostgreSQLNotificationRejectsAmbiguousSignedJSON(t *testing.T) {
	db, ctx := consumerDatabase(t)
	data := store.Store{DB: db}
	if err := data.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	source, generation, notice, box := fixtureUUID(t), fixtureUUID(t), fixtureUUID(t), fixtureUUID(t)
	secret := strings.Repeat("a", 64)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := db.ExecContext(ctx, `DELETE FROM mail.ingress_notice WHERE source_id=$1`, source); err != nil {
			t.Error(err)
		}
	})
	raw := `{"schema":"anas.mail-notification/v1","source_id":"` + source + `","generation":"` + generation + `","notification_id":"` + notice + `","message_id":"message-1","mailbox_id":"` + box + `"}`
	handler := ingress.NotificationHandler{Store: data, Sources: map[string]ingress.NotificationSource{source: {Generation: generation, Secret: secret}}}
	send := func(body string) int {
		r := httptest.NewRequest("POST", "https://mail.example.test/api/ingress/notify", bytes.NewBufferString(body))
		stamp := strconv.FormatInt(time.Now().Unix(), 10)
		key, _ := hex.DecodeString(secret)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte(stamp + "." + body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-ANAS-Mail-Time", stamp)
		r.Header.Set("X-ANAS-Mail-Signature", hex.EncodeToString(mac.Sum(nil)))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w.Code
	}
	invalid := strings.Replace(raw, `"source_id":`, `"SOURCE_ID":"`+generation+`","source_id":`, 1)
	if status := send(invalid); status != 400 {
		t.Fatal("ambiguous notice accepted", status)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_notice WHERE source_id=$1`, source).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid notice reached database", err, count)
	}
	if send(raw) != 202 || send(raw) != 202 {
		t.Fatal("valid notice not accepted")
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM mail.ingress_notice WHERE source_id=$1`, source).Scan(&count); err != nil || count != 1 {
		t.Fatal("valid notice deduplication", err, count)
	}
}
