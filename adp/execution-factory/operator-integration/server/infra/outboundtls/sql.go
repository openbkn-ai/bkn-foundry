// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package outboundtls

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

type SQLDB interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (s SQLStore) List(ctx context.Context, scope string) (map[string]Policy, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT f_origin, f_fingerprint, f_skip_verify FROM t_outbound_tls_trust WHERE f_registration = ?", scope)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	policies := make(map[string]Policy)
	for rows.Next() {
		var origin string
		var p Policy
		var skip int
		if err := rows.Scan(&origin, &p.Fingerprint, &skip); err != nil {
			return nil, err
		}
		p.SkipVerify = skip != 0
		if err := validate(p); err != nil {
			return nil, err
		}
		policies[origin] = p
	}
	return policies, rows.Err()
}

type SQLStore struct{ DB SQLDB }

func (s SQLStore) Remove(ctx context.Context, scope string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM t_outbound_tls_trust WHERE f_registration = ?", scope)
	return err
}

func trustKey(scope, origin string) string {
	s := sha256.Sum256([]byte(scope + "\x00" + origin))
	return hex.EncodeToString(s[:])
}

func (s SQLStore) Get(ctx context.Context, scope, origin string) (Policy, bool, error) {
	var p Policy
	var skip int
	err := s.DB.QueryRowContext(ctx, "SELECT f_fingerprint, f_skip_verify FROM t_outbound_tls_trust WHERE f_id = ?", trustKey(scope, origin)).Scan(&p.Fingerprint, &skip)
	if errors.Is(err, sql.ErrNoRows) {
		return p, false, nil
	}
	p.SkipVerify = skip != 0
	return p, err == nil, err
}

func (s SQLStore) Create(ctx context.Context, scope, origin string, p Policy) error {
	_, err := s.DB.ExecContext(ctx, "INSERT INTO t_outbound_tls_trust (f_id, f_registration, f_origin, f_fingerprint, f_skip_verify) VALUES (?, ?, ?, ?, ?)", trustKey(scope, origin), scope, origin, p.Fingerprint, skipInt(p))
	if err != nil {
		// A concurrent enrollment must never overwrite the first saved decision.
		got, ok, readErr := s.Get(ctx, scope, origin)
		if readErr == nil && ok {
			if got == p {
				return nil
			}
			return ErrConflict
		}
	}
	return err
}

func skipInt(p Policy) int {
	if p.SkipVerify {
		return 1
	}
	return 0
}

func (s SQLStore) Replace(ctx context.Context, scope, origin string, old, next Policy) error {
	if old == next {
		got, ok, err := s.Get(ctx, scope, origin)
		if err != nil {
			return err
		}
		if !ok || got != old {
			return ErrConflict
		}
		return nil
	}
	result, err := s.DB.ExecContext(ctx, "UPDATE t_outbound_tls_trust SET f_fingerprint = ?, f_skip_verify = ? WHERE f_id = ? AND f_fingerprint = ? AND f_skip_verify = ?", next.Fingerprint, skipInt(next), trustKey(scope, origin), old.Fingerprint, skipInt(old))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return ErrConflict
	}
	return err
}
