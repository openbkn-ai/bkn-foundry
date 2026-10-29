package outboundtls

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestSQLStoreEnrollmentAndCompareAndSwap(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	s := SQLStore{DB: db}
	ctx := context.Background()
	scope := "mcp:one"
	origin := "https://example.com:443"
	key := trustKey(scope, origin)
	old := Policy{Fingerprint: strings.Repeat("a", 64)}
	next := Policy{Fingerprint: strings.Repeat("b", 64)}
	mock.ExpectQuery("SELECT f_fingerprint").WithArgs(key).WillReturnError(sql.ErrNoRows)
	if _, ok, err := s.Get(ctx, scope, origin); err != nil || ok {
		t.Fatalf("lookup: %v %v", ok, err)
	}
	mock.ExpectExec("INSERT INTO t_outbound_tls_trust").WithArgs(key, scope, origin, old.Fingerprint, 0).WillReturnResult(sqlmock.NewResult(1, 1))
	if err := s.Create(ctx, scope, origin, old); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE t_outbound_tls_trust").WithArgs(next.Fingerprint, 0, key, old.Fingerprint, 0).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.Replace(ctx, scope, origin, old, next); err != nil {
		t.Fatal(err)
	}
	mock.ExpectExec("UPDATE t_outbound_tls_trust").WithArgs(next.Fingerprint, 0, key, old.Fingerprint, 0).WillReturnResult(sqlmock.NewResult(0, 0))
	if err := s.Replace(ctx, scope, origin, old, next); !errors.Is(err, ErrConflict) {
		t.Fatal("stale update accepted:", err)
	}
	// A concurrent first registration cannot overwrite the winner's certificate.
	mock.ExpectExec("INSERT INTO t_outbound_tls_trust").WillReturnError(errors.New("duplicate key"))
	mock.ExpectQuery("SELECT f_fingerprint").WithArgs(key).WillReturnRows(sqlmock.NewRows([]string{"f_fingerprint", "f_skip_verify"}).AddRow(old.Fingerprint, 0))
	if err := s.Create(ctx, scope, origin, next); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent enrollment overwrote pin:", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapClosesRegistrationCursorBeforeTrustWrites(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	upstream, _ := tlsServer(t, certificate(t, "ingress.local", false))
	m, _ := New(Config{}, SQLStore{DB: db})
	origin, _ := Origin(upstream.URL)
	mock.ExpectQuery("SELECT registration").WillReturnRows(sqlmock.NewRows([]string{"scope", "url"}).AddRow("mcp:one", upstream.URL)).RowsWillBeClosed()
	mock.ExpectQuery("SELECT f_fingerprint").WithArgs(trustKey("mcp:one", origin)).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO t_outbound_tls_trust").WillReturnResult(sqlmock.NewResult(1, 1))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.BootstrapQuery(ctx, db, "SELECT registration"); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistrationTrustUsesTheSuppliedTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	m, _ := New(Config{}, SQLStore{DB: db})
	old := Default()
	SetDefault(m)
	defer SetDefault(old)
	upstream, _ := tlsServer(t, certificate(t, "ingress.local", false))
	origin, _ := Origin(upstream.URL)
	key := trustKey("mcp:new", origin)
	mock.ExpectBegin()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	mock.ExpectQuery("SELECT f_fingerprint").WithArgs(key).WillReturnError(sql.ErrNoRows)
	mock.ExpectExec("INSERT INTO t_outbound_tls_trust").WithArgs(key, "mcp:new", origin, sqlmock.AnyArg(), 0).WillReturnResult(sqlmock.NewResult(1, 1))
	if err := Apply(context.Background(), "mcp:new", upstream.URL, nil, SQLStore{DB: tx}); err != nil {
		t.Fatal(err)
	}
	// The registration caller can roll back this decision along with its row.
	mock.ExpectRollback()
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
