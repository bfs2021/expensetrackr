package store

import (
	"database/sql"
	"fmt"
)

// Migration 2 -> 3: per-user ownership of balances and transactions (issue #62).
//
// Existing rows (pre-production data) are assigned to the earliest-created
// user (ORDER BY created_at ASC, id ASC) when at least one user exists. If
// the database has balances or transactions but no users, those orphan rows
// are deleted so the NOT NULL user_uid column can be applied without failing.
// Balance names become unique per user (was globally unique).
//
// Indexes are created after the old table is dropped so names that already
// exist on the v2 tables (idx_balances_type, …) do not collide.

const balancesV3Table = `
CREATE TABLE balances_v3 (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    uid           TEXT    NOT NULL UNIQUE CHECK (length(uid) = 36),
    user_uid      TEXT    NOT NULL REFERENCES users (uid) ON DELETE CASCADE,
    name          TEXT    NOT NULL COLLATE NOCASE CHECK (length(trim(name)) > 0),
    type          TEXT    NOT NULL CHECK (type IN ('payment_account', 'credit_card', 'other_asset', 'other_liability')),
    currency      TEXT    NOT NULL CHECK (length(currency) = 3),
    description   TEXT    NOT NULL DEFAULT '',
    amount_scale  INTEGER NOT NULL CHECK (amount_scale BETWEEN 0 AND 4),
    balance_minor INTEGER,
    debt_minor    INTEGER,
    limit_minor   INTEGER CHECK (limit_minor >= 0),
    created_at    TEXT    NOT NULL,
    updated_at    TEXT,
    version       INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    CHECK (
        (type IN ('payment_account', 'other_asset')
            AND balance_minor IS NOT NULL AND debt_minor IS NULL AND limit_minor IS NULL)
        OR
        (type IN ('credit_card', 'other_liability')
            AND balance_minor IS NULL AND debt_minor IS NOT NULL AND limit_minor IS NOT NULL)
    ),
    UNIQUE (user_uid, name COLLATE NOCASE)
);
`

const balancesV3Indexes = `
CREATE INDEX idx_balances_type ON balances (type, name);
CREATE UNIQUE INDEX idx_balances_uid ON balances (uid);
CREATE INDEX idx_balances_user_uid ON balances (user_uid);
`

const transactionsV3Table = `
CREATE TABLE transactions_v3 (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    uid           TEXT    NOT NULL UNIQUE CHECK (length(uid) = 36),
    user_uid      TEXT    NOT NULL REFERENCES users (uid) ON DELETE CASCADE,
    amount_minor  INTEGER NOT NULL CHECK (amount_minor > 0),
    amount_scale  INTEGER NOT NULL CHECK (amount_scale BETWEEN 0 AND 4),
    currency      TEXT    NOT NULL CHECK (length(currency) = 3),
    balance_uid   TEXT    NOT NULL CHECK (length(balance_uid) = 36),
    account       TEXT    NOT NULL CHECK (length(account) > 0),
    type          TEXT    NOT NULL DEFAULT 'expense' CHECK (type IN ('expense', 'income', 'transfer', 'asset_purchase', 'balance_adjustment')),
    to_balance_uid TEXT   CHECK (to_balance_uid IS NULL OR length(to_balance_uid) = 36),
    to_account    TEXT,
    category_uid  TEXT    CHECK (category_uid IS NULL OR length(category_uid) = 36),
    spent_at      TEXT    NOT NULL,
    spent_at_unix INTEGER NOT NULL,
    note          TEXT    NOT NULL DEFAULT '',
    created_at    TEXT    NOT NULL,
    updated_at    TEXT,
    version       INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    balance_applied INTEGER NOT NULL DEFAULT 0 CHECK (balance_applied IN (0, 1)),
    adjustment_direction TEXT CHECK (adjustment_direction IS NULL OR adjustment_direction IN ('increase', 'decrease')),
    to_amount_minor INTEGER CHECK (to_amount_minor IS NULL OR to_amount_minor > 0),
    to_amount_scale INTEGER CHECK (to_amount_scale IS NULL OR to_amount_scale BETWEEN 0 AND 4),
    to_currency   TEXT    CHECK (to_currency IS NULL OR length(to_currency) = 3),
    exchange_rate TEXT,
    rate_source   TEXT    CHECK (rate_source IS NULL OR rate_source IN ('frankfurter', 'manual')),
    rate_date     TEXT    CHECK (rate_date IS NULL OR length(rate_date) = 10),
    import_ref    TEXT    CHECK (import_ref IS NULL OR length(import_ref) BETWEEN 1 AND 200),
    CHECK ((type = 'balance_adjustment') = (adjustment_direction IS NOT NULL)),
    CHECK (type <> 'balance_adjustment' OR (to_balance_uid IS NULL AND category_uid IS NULL AND balance_applied = 0))
);
`

const transactionsV3Indexes = `
CREATE INDEX idx_transactions_spent_at_unix ON transactions (spent_at_unix DESC, id DESC);
CREATE INDEX idx_transactions_balance_uid ON transactions (balance_uid);
CREATE UNIQUE INDEX idx_transactions_uid ON transactions (uid);
CREATE INDEX idx_transactions_type ON transactions (type, spent_at_unix DESC);
CREATE INDEX idx_transactions_category_uid ON transactions (category_uid);
CREATE INDEX idx_transactions_to_balance_uid ON transactions (to_balance_uid);
CREATE UNIQUE INDEX idx_transactions_import_ref ON transactions (balance_uid, import_ref) WHERE import_ref IS NOT NULL;
CREATE INDEX idx_transactions_user_uid ON transactions (user_uid);
`

// earliestUserUID returns the uid of the earliest-created user, or "" if none.
func earliestUserUID(tx *sql.Tx) (string, error) {
	var uid string
	err := tx.QueryRow(`SELECT uid FROM users ORDER BY created_at ASC, id ASC LIMIT 1`).Scan(&uid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up earliest user: %w", err)
	}
	return uid, nil
}

// syncAutoIncrement sets sqlite_sequence for table to MAX(id) so the next
// AUTOINCREMENT value continues after a rebuild. No-op when the table is empty
// or sqlite_sequence is not present yet.
func syncAutoIncrement(tx *sql.Tx, table string) error {
	var max sql.NullInt64
	// table is a fixed identifier from our migration, never user input.
	if err := tx.QueryRow(`SELECT MAX(id) FROM ` + table).Scan(&max); err != nil {
		return fmt.Errorf("max id of %s: %w", table, err)
	}
	if !max.Valid {
		return nil
	}
	if _, err := tx.Exec(`DELETE FROM sqlite_sequence WHERE name = ?`, table); err != nil {
		return fmt.Errorf("clear sqlite_sequence for %s: %w", table, err)
	}
	if _, err := tx.Exec(`INSERT INTO sqlite_sequence (name, seq) VALUES (?, ?)`, table, max.Int64); err != nil {
		return fmt.Errorf("write sqlite_sequence for %s: %w", table, err)
	}
	return nil
}

// rebuildOwnedTable creates tmpName from createSQL, optionally copies rows
// from oldName (when owner is non-empty), drops oldName (and its indexes),
// renames tmpName to oldName, syncs AUTOINCREMENT, then runs indexSQL against
// the final table name.
func rebuildOwnedTable(tx *sql.Tx, oldName, tmpName, createSQL, copySQL, indexSQL string, owner string, copyArgs ...any) error {
	if _, err := tx.Exec(createSQL); err != nil {
		return fmt.Errorf("create %s: %w", tmpName, err)
	}
	if owner != "" {
		if _, err := tx.Exec(copySQL, copyArgs...); err != nil {
			return fmt.Errorf("copy %s: %w", oldName, err)
		}
	}
	if _, err := tx.Exec(`DROP TABLE ` + oldName); err != nil {
		return fmt.Errorf("drop %s: %w", oldName, err)
	}
	if _, err := tx.Exec(`ALTER TABLE ` + tmpName + ` RENAME TO ` + oldName); err != nil {
		return fmt.Errorf("rename %s: %w", tmpName, err)
	}
	if err := syncAutoIncrement(tx, oldName); err != nil {
		return err
	}
	if _, err := tx.Exec(indexSQL); err != nil {
		return fmt.Errorf("indexes for %s: %w", oldName, err)
	}
	return nil
}

func addUserOwnership(tx *sql.Tx) error {
	owner, err := earliestUserUID(tx)
	if err != nil {
		return err
	}
	if owner == "" {
		// No user to own the rows: drop orphans so NOT NULL can be applied.
		if _, err := tx.Exec(`DELETE FROM transactions`); err != nil {
			return fmt.Errorf("delete orphan transactions: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM balances`); err != nil {
			return fmt.Errorf("delete orphan balances: %w", err)
		}
	}

	if err := rebuildOwnedTable(tx, "balances", "balances_v3", balancesV3Table, `
		INSERT INTO balances_v3 (
			id, uid, user_uid, name, type, currency, description, amount_scale,
			balance_minor, debt_minor, limit_minor, created_at, updated_at, version
		)
		SELECT
			id, uid, ?, name, type, currency, description, amount_scale,
			balance_minor, debt_minor, limit_minor, created_at, updated_at, version
		FROM balances`, balancesV3Indexes, owner, owner); err != nil {
		return err
	}

	if err := rebuildOwnedTable(tx, "transactions", "transactions_v3", transactionsV3Table, `
		INSERT INTO transactions_v3 (
			id, uid, user_uid, amount_minor, amount_scale, currency, balance_uid, account, type,
			to_balance_uid, to_account, category_uid, spent_at, spent_at_unix, note,
			created_at, updated_at, version, balance_applied, adjustment_direction,
			to_amount_minor, to_amount_scale, to_currency, exchange_rate, rate_source, rate_date, import_ref
		)
		SELECT
			id, uid, ?, amount_minor, amount_scale, currency, balance_uid, account, type,
			to_balance_uid, to_account, category_uid, spent_at, spent_at_unix, note,
			created_at, updated_at, version, balance_applied, adjustment_direction,
			to_amount_minor, to_amount_scale, to_currency, exchange_rate, rate_source, rate_date, import_ref
		FROM transactions`, transactionsV3Indexes, owner, owner); err != nil {
		return err
	}
	return nil
}
