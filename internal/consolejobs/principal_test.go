package consolejobs

import "testing"

func TestTransactionPrincipalRoundTripRejectsPrefixCollisions(t *testing.T) {
	for _, kind := range []PrincipalKind{PrincipalBootstrap, PrincipalEnrollment} {
		value, err := TransactionPrincipal(kind, "txn-1.ok")
		if err != nil {
			t.Fatal(err)
		}
		gotKind, transactionID, ok := ParseTransactionPrincipal(value)
		if !ok || gotKind != kind || transactionID != "txn-1.ok" {
			t.Fatalf("ParseTransactionPrincipal(%q) = %q, %q, %v", value, gotKind, transactionID, ok)
		}
	}

	for _, value := range []string{
		"", "bootstrap", "bootstrap:", "bootstrap:txn:other", "bootstrapper:txn",
		"enrollment:txn/other", "owner:txn", " bootstrap:txn", "bootstrap:txn ",
	} {
		if _, _, ok := ParseTransactionPrincipal(value); ok {
			t.Errorf("ParseTransactionPrincipal(%q) unexpectedly succeeded", value)
		}
	}
	for _, test := range []struct {
		kind PrincipalKind
		txn  string
	}{
		{kind: "owner", txn: "txn"},
		{kind: PrincipalBootstrap, txn: "txn:other"},
		{kind: PrincipalEnrollment, txn: ""},
	} {
		if _, err := TransactionPrincipal(test.kind, test.txn); err == nil {
			t.Errorf("TransactionPrincipal(%q, %q) unexpectedly succeeded", test.kind, test.txn)
		}
	}
}
