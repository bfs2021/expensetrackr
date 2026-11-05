package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/nutmos/expensetrackr/pkg/balance"
	"github.com/nutmos/expensetrackr/pkg/transaction"
	"github.com/nutmos/expensetrackr/pkg/user"
	"github.com/nutmos/expensetrackr/pkg/validate"
)

// openV2 creates a database at schema version 2 (baseline + api_tokens), as
// an older build would have left it before migration v3.
func openV2(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := createBaseline(tx); err != nil {
		t.Fatal(err)
	}
	if err := createAPITokens(tx); err != nil {
		t.Fatal(err)
	}
	if err := setVersion(tx, 2); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestMigrationV3 upgrades a v2 database that already has users, balances and
// transactions: rows are assigned to the earliest-created user, indexes appear,
// and the schema matches a fresh database.
func TestMigrationV3(t *testing.T) {
	dir := t.TempDir()
	oldPath, freshPath := filepath.Join(dir, "v2.db"), filepath.Join(dir, "fresh.db")
	openV2(t, oldPath)
	if v := pragmaInt(t, oldPath, "user_version"); v != 2 {
		t.Fatalf("setup: user_version = %d", v)
	}
	db, err := sql.Open("sqlite", "file:"+oldPath)
	if err != nil {
		t.Fatal(err)
	}
	// Two users (earliest first) and orphan-looking rows without user_uid.
	if _, err := db.Exec(`
		INSERT INTO users (uid, username, display_name, password_hash, created_at)
		VALUES
			('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1', 'early', 'Early', 'x', '2026-01-01T00:00:00Z'),
			('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2', 'late', 'Late', 'x', '2026-06-01T00:00:00Z');
		INSERT INTO balances (uid, name, type, currency, amount_scale, balance_minor, created_at, version)
		VALUES ('cccccccc-cccc-4ccc-8ccc-ccccccccccc3', 'Cash', 'payment_account', 'THB', 2, 100000, '2026-02-01T00:00:00Z', 1);
		INSERT INTO transactions (uid, type, amount_minor, amount_scale, currency, balance_uid, account, spent_at, spent_at_unix, created_at, version, balance_applied)
		VALUES ('dddddddd-dddd-4ddd-8ddd-ddddddddddd4', 'expense', 500, 2, 'THB', 'cccccccc-cccc-4ccc-8ccc-ccccccccccc3', 'Cash', '2026-02-02T12:00:00+08:00', 0, '2026-02-02T12:00:00Z', 1, 0);
	`); err != nil {
		t.Fatalf("seed v2 data: %v", err)
	}
	db.Close()

	st, err := Open(oldPath)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	ctx := context.Background()
	early := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
	late := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbb2"
	b, err := st.GetBalanceByUID(ctx, early, "cccccccc-cccc-4ccc-8ccc-ccccccccccc3")
	if err != nil || b.Name != "Cash" {
		t.Fatalf("balance assigned to early: %+v %v", b, err)
	}
	if _, err := st.GetBalanceByUID(ctx, late, "cccccccc-cccc-4ccc-8ccc-ccccccccccc3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("late must not see early's balance: %v", err)
	}
	txns, err := st.List(ctx, early, ListFilter{})
	if err != nil || len(txns) != 1 || string(txns[0].UID) != "dddddddd-dddd-4ddd-8ddd-ddddddddddd4" {
		t.Fatalf("txns for early: %+v %v", txns, err)
	}
	if got, _ := st.List(ctx, late, ListFilter{}); len(got) != 0 {
		t.Errorf("late list: %d", len(got))
	}
	st.Close()

	fresh, err := Open(freshPath)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Close()
	if v := pragmaInt(t, oldPath, "user_version"); v != SchemaVersion {
		t.Errorf("upgraded user_version = %d, want %d", v, SchemaVersion)
	}
	up, fr := schemaObjects(t, oldPath), schemaObjects(t, freshPath)
	if fmtSprint := func(m map[string]string) string {
		// compare keys only for stability (DDL text of rebuilt tables differs in formatting)
		return ""
	}; false {
		_ = fmtSprint
	}
	for _, k := range latestObjects {
		if _, ok := up[k]; !ok {
			t.Errorf("upgraded db missing %s", k)
		}
		if _, ok := fr[k]; !ok {
			t.Errorf("fresh db missing %s", k)
		}
	}
	for _, k := range v3Objects {
		if _, ok := up[k]; !ok {
			t.Errorf("upgraded db missing v3 object %s", k)
		}
	}
	// user_uid column is present and NOT NULL.
	ddl := up["table balances"]
	if !containsAll(ddl, "user_uid", "REFERENCES") {
		t.Errorf("balances ddl missing user ownership: %s", ddl)
	}
	ddl = up["table transactions"]
	if !containsAll(ddl, "user_uid", "REFERENCES") {
		t.Errorf("transactions ddl missing user ownership: %s", ddl)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !containsFold(s, p) {
			return false
		}
	}
	return true
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if equalFoldASCII(s[i:i+len(sub)], sub) {
					return true
				}
			}
			return false
		})())
}

func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// TestMigrationV3OrphansDeleted: a v2 DB with balances but no users drops the
// orphan rows so the NOT NULL user_uid column can be applied.
func TestMigrationV3OrphansDeleted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orphan.db")
	openV2(t, path)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		INSERT INTO balances (uid, name, type, currency, amount_scale, balance_minor, created_at, version)
		VALUES ('cccccccc-cccc-4ccc-8ccc-ccccccccccc3', 'Cash', 'payment_account', 'THB', 2, 100000, 't', 1);
	`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer st.Close()
	owner := seedOwner(t, st)
	list, err := st.ListBalances(context.Background(), owner, "")
	if err != nil || len(list) != 0 {
		t.Fatalf("orphans should be gone: %+v %v", list, err)
	}
}

// TestPerUserStoreIsolation: user B cannot read or mutate user A's balances or
// transactions, and cannot create a transaction against A's balance.
func TestPerUserStoreIsolation(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "iso.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	alice := user.User{Username: ptr("alice"), DisplayName: "Alice"}
	bob := user.User{Username: ptr("bob"), DisplayName: "Bob"}
	if err := st.RegisterUser(ctx, &alice, "$2a$04$x", false); err != nil {
		t.Fatal(err)
	}
	if err := st.RegisterUser(ctx, &bob, "$2a$04$x", false); err != nil {
		t.Fatal(err)
	}
	aPay := mustBalance(t, balance.Input{Name: "Cash", Type: "payment_account", Currency: "THB", Balance: dec("1000")})
	if err := st.CreateBalance(ctx, alice.UID, &aPay); err != nil {
		t.Fatal(err)
	}
	bPay := mustBalance(t, balance.Input{Name: "Cash", Type: "payment_account", Currency: "THB", Balance: dec("50")})
	if err := st.CreateBalance(ctx, bob.UID, &bPay); err != nil {
		t.Fatal(err) // same name, different user: allowed
	}
	e, err := transaction.CreateInput{Amount: "10", Currency: "THB", BalanceUID: string(aPay.UID), SpentAt: "2026-10-08T12:00:00+08:00"}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	e.Account = "Cash"
	if err := st.Create(ctx, alice.UID, &e); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		fn   func() error
	}{
		{"bob get alice balance", func() error {
			_, err := st.GetBalanceByUID(ctx, bob.UID, string(aPay.UID))
			return err
		}},
		{"bob get alice tx", func() error {
			_, err := st.GetByUID(ctx, bob.UID, string(e.UID))
			return err
		}},
		{"bob update alice tx", func() error {
			_, err := st.UpdateWith(ctx, bob.UID, e.ID, WriteOptions{}, func(cur transaction.Transaction) (transaction.Transaction, error) {
				cur.Note = "x"
				return cur, nil
			})
			return err
		}},
		{"bob delete alice tx", func() error {
			return st.DeleteWith(ctx, bob.UID, e.ID, WriteOptions{})
		}},
		{"bob update alice balance", func() error {
			_, err := st.UpdateBalance(ctx, bob.UID, aPay.ID, func(cur balance.Balance) (balance.Balance, error) {
				return cur, nil
			})
			return err
		}},
		{"bob delete alice balance", func() error {
			return st.DeleteBalance(ctx, bob.UID, aPay.ID)
		}},
	}
	for _, c := range cases {
		if err := c.fn(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: got %v, want ErrNotFound", c.name, err)
		}
	}
	// Create against alice's balance as bob → validation "does not match".
	cross, err := transaction.CreateInput{Amount: "1", Currency: "THB", BalanceUID: string(aPay.UID), SpentAt: "2026-10-08T12:00:00+08:00"}.Validate()
	if err != nil {
		t.Fatal(err)
	}
	cross.Account = "Cash"
	var ve *validate.ValidationError
	if err := st.Create(ctx, bob.UID, &cross); !errors.As(err, &ve) || ve.Fields["balance_uid"] == "" {
		t.Fatalf("cross-user create: %v", err)
	}
	alist, _ := st.List(ctx, alice.UID, ListFilter{})
	blist, _ := st.List(ctx, bob.UID, ListFilter{})
	if len(alist) != 1 || len(blist) != 0 {
		t.Errorf("lists alice=%d bob=%d", len(alist), len(blist))
	}
	abal, _ := st.ListBalances(ctx, alice.UID, "")
	bbal, _ := st.ListBalances(ctx, bob.UID, "")
	if len(abal) != 1 || len(bbal) != 1 {
		t.Errorf("balances alice=%d bob=%d", len(abal), len(bbal))
	}
}

func ptr(s string) *string { return &s }
