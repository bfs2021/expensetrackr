package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/nutmos/expensetrackr/pkg/common_type"
)

// ErrTokenNotFound is returned when no active API token matches.
var ErrTokenNotFound = errors.New("api token not found")

// APIToken is a stored personal API token. The plaintext is never stored;
// TokenHash is its SHA-256.
type APIToken struct {
	ID         int64      `json:"-"`
	UID        string     `json:"uid"`
	UserUID    string     `json:"-"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	TokenHash  string     `json:"-"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"-"`
}

// HasScope reports whether the token carries scope.
func (t APIToken) HasScope(scope string) bool {
	return slices.Contains(t.Scopes, scope)
}

const apiTokenCols = `id, uid, user_uid, name, prefix, token_hash, scopes, created_at, last_used_at, expires_at, revoked_at`

func scanAPIToken(row interface{ Scan(...any) error }) (APIToken, error) {
	var t APIToken
	var scopes, created string
	var used, exp, rev sql.NullString
	if err := row.Scan(&t.ID, &t.UID, &t.UserUID, &t.Name, &t.Prefix, &t.TokenHash, &scopes, &created, &used, &exp, &rev); err != nil {
		return t, err
	}
	t.Scopes = strings.Fields(scopes)
	t.CreatedAt, _ = time.Parse(time.RFC3339, created)
	t.LastUsedAt, t.ExpiresAt, t.RevokedAt = parseNullTime(used), parseNullTime(exp), parseNullTime(rev)
	return t, nil
}

func parseNullTime(s sql.NullString) *time.Time {
	if !s.Valid {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s.String)
	if err != nil {
		return nil
	}
	return &t
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

// CreateAPIToken stores t (TokenHash, UserUID, Name, Prefix, Scopes and
// optionally ExpiresAt set by the caller) and fills ID, UID and CreatedAt.
func (s *Store) CreateAPIToken(ctx context.Context, t *APIToken) error {
	uid, err := common_type.NewUID()
	if err != nil {
		return err
	}
	now := s.nowRFC3339()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (uid, user_uid, name, prefix, token_hash, scopes, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		uid.String(), t.UserUID, t.Name, t.Prefix, t.TokenHash, strings.Join(t.Scopes, " "), now, nullTime(t.ExpiresAt))
	if err != nil {
		return fmt.Errorf("create api token: %w", err)
	}
	if t.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	t.UID = uid.String()
	t.CreatedAt, _ = time.Parse(time.RFC3339, now)
	return nil
}

// ListAPITokens returns the user's tokens that are not revoked, newest first.
func (s *Store) ListAPITokens(ctx context.Context, userUID string) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+apiTokenCols+` FROM api_tokens WHERE user_uid = ? AND revoked_at IS NULL ORDER BY created_at DESC, id DESC`, userUID)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer rows.Close()
	out := []APIToken{}
	for rows.Next() {
		t, err := scanAPIToken(rows)
		if err != nil {
			return nil, fmt.Errorf("list api tokens: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetAPITokenByHash returns the token with this hash (revoked and expired
// ones too; the caller checks), or ErrTokenNotFound.
func (s *Store) GetAPITokenByHash(ctx context.Context, tokenHash string) (APIToken, error) {
	t, err := scanAPIToken(s.db.QueryRowContext(ctx, `SELECT `+apiTokenCols+` FROM api_tokens WHERE token_hash = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrTokenNotFound
	}
	if err != nil {
		return t, fmt.Errorf("get api token: %w", err)
	}
	return t, nil
}

// TouchAPIToken records when a token was last used.
func (s *Store) TouchAPIToken(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, ts(at), id)
	return err
}

// RevokeAPIToken revokes the user's active token uid. A token that belongs
// to another user, is already revoked or does not exist gives
// ErrTokenNotFound.
func (s *Store) RevokeAPIToken(ctx context.Context, userUID, uid string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE api_tokens SET revoked_at = ? WHERE uid = ? AND user_uid = ? AND revoked_at IS NULL`, s.nowRFC3339(), uid, userUID)
	if err != nil {
		return fmt.Errorf("revoke api token: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTokenNotFound
	}
	return nil
}
