package store

import (
	"database/sql"
	"fmt"
)

// apiTokensSchema is migration 1 -> 2: personal API tokens (docs/auth.md).
//
// Table "api_tokens": one row per token, owned by one user.
//   - uid: public UUID, used to revoke the token.
//   - prefix: the first characters of the token (e.g. "ext_AbCd1234"),
//     non-secret, shown in lists so a user can tell tokens apart.
//   - token_hash: SHA-256 (hex) of the full token; the plaintext is never
//     stored.
//   - scopes: space-separated scope names ("read", "write").
//   - created_at / last_used_at / expires_at / revoked_at: RFC 3339 UTC;
//     expires_at NULL means no expiry, revoked_at NULL means active.
const apiTokensSchema = `
CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    uid          TEXT NOT NULL UNIQUE,
    user_uid     TEXT NOT NULL REFERENCES users (uid) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE CHECK (length(token_hash) = 64),
    scopes       TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    expires_at   TEXT,
    revoked_at   TEXT
);
CREATE INDEX idx_api_tokens_user_uid ON api_tokens (user_uid);
`

func createAPITokens(tx *sql.Tx) error {
	if _, err := tx.Exec(apiTokensSchema); err != nil {
		return fmt.Errorf("create api_tokens: %w", err)
	}
	return nil
}
