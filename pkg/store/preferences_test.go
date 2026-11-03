package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nutmos/expensetrackr/pkg/user"
)

// TestUserPreferencesRoundTrip checks what reaches the users.preferences
// column and what comes back, including rows written by other versions.
func TestUserPreferencesRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	u := user.User{DisplayName: "Prefs"}
	if err := st.CreateUser(ctx, &u); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		write      func() error
		wantColumn string // "" = don't check
		want       user.Preferences
	}{
		{"new user stores {}", func() error { return nil }, `{}`, user.Preferences{}},
		{"set via UpdateUser", func() error {
			_, err := st.UpdateUser(ctx, u.ID, func(cur user.User) (user.User, error) {
				cur.Preferences = user.Preferences{BaseCurrency: "THB"}
				return cur, nil
			})
			return err
		}, `{"base_currency":"THB"}`, user.Preferences{BaseCurrency: "THB"}},
		{"unknown keys in the row are tolerated", func() error {
			_, err := st.db.ExecContext(ctx, `UPDATE users SET preferences = '{"theme":"dark","base_currency":"EUR"}' WHERE id = ?`, u.ID)
			return err
		}, "", user.Preferences{BaseCurrency: "EUR"}},
		{"missing keys are tolerated", func() error {
			_, err := st.db.ExecContext(ctx, `UPDATE users SET preferences = '{"theme":"dark"}' WHERE id = ?`, u.ID)
			return err
		}, "", user.Preferences{}},
		{"reset stores {}", func() error {
			_, err := st.UpdateUser(ctx, u.ID, func(cur user.User) (user.User, error) {
				cur.Preferences = user.Preferences{}
				return cur, nil
			})
			return err
		}, `{}`, user.Preferences{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.write(); err != nil {
				t.Fatal(err)
			}
			if tc.wantColumn != "" {
				var col string
				if err := st.db.QueryRowContext(ctx, `SELECT preferences FROM users WHERE id = ?`, u.ID).Scan(&col); err != nil || col != tc.wantColumn {
					t.Errorf("column %q %v, want %q", col, err, tc.wantColumn)
				}
			}
			got, err := st.GetUserByUID(ctx, u.UID)
			if err != nil || got.Preferences != tc.want {
				t.Errorf("read %+v %v, want %+v", got.Preferences, err, tc.want)
			}
		})
	}
}
