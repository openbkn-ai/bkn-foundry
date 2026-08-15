package sqlx

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"
)

/*
The database/sql pool uses MaxIdleConns and MaxOpenConns. When the maximum
open count is much larger than the idle count, high traffic can repeatedly
create and close connections, which wastes resources and exhausts ephemeral
ports. Prefer MaxIdleConns == MaxOpenConns for services with sustained load.

ConnMaxIdleTime limits how long an idle connection is retained. It should be
less than the database server's wait_timeout. ConnMaxLifetime is useful only
when the database driver does not already provide timeout, readTimeout, and
writeTimeout controls.

Recommended defaults:
1. MaxIdleConns == MaxOpenConns
2. ConnMaxIdleTime = 120s

The 120-second interval allows the kernel to reclaim recently closed
TIME_WAIT connections.
*/

//go:generate mockgen -package mock -source ./db.go -destination ./mock/mock_db.go

type reader interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	SetConnMaxIdleTime(d time.Duration)
	SetConnMaxLifetime(d time.Duration)
	SetMaxIdleConns(n int)
	SetMaxOpenConns(n int)
	Close() error
}

type writer interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	Prepare(query string) (*sql.Stmt, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	Begin() (*sql.Tx, error)
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
	Ping() error
	PingContext(ctx context.Context) error
	SetConnMaxIdleTime(d time.Duration)
	SetConnMaxLifetime(d time.Duration)
	SetMaxIdleConns(n int)
	SetMaxOpenConns(n int)
	Close() error
}

type DB struct {
	reader
	writer
}

// ParseHost wraps an IPv6 host in brackets.
func ParseHost(host string) string {
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]", host)
	}

	return host
}

// NewDB creates a database connection.
func NewDB(dbConfig *DBConfig) (*DB, error) {
	driverName := "openbkn-rds"
	if dbConfig.CustomDriver != "" {
		driverName = dbConfig.CustomDriver
	}

	query := url.Values{}
	if dbConfig.Charset != "" {
		query.Set("charset", dbConfig.Charset)
	}
	if dbConfig.Timeout > 0 {
		query.Set("timeout", fmt.Sprintf("%ds", dbConfig.Timeout))
	}
	if dbConfig.ReadTimeout > 0 {
		query.Set("readTimeout", fmt.Sprintf("%ds", dbConfig.ReadTimeout))
	}
	if dbConfig.WriteTimeout > 0 {
		query.Set("writeTimeout", fmt.Sprintf("%ds", dbConfig.WriteTimeout))
	}
	if dbConfig.ParseTime != "" {
		query.Set("parseTime", dbConfig.ParseTime)
	}
	if dbConfig.Loc != "" {
		query.Set("loc", dbConfig.Loc)
	}
	dbConfig.Host = ParseHost(dbConfig.Host)
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?%s",
		dbConfig.User,
		dbConfig.Password,
		dbConfig.Host,
		dbConfig.Port,
		dbConfig.Database,
		query.Encode())
	w, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	if dbConfig.MaxOpenConns == 0 {
		dbConfig.MaxOpenConns = 10
	}
	w.SetMaxOpenConns(dbConfig.MaxOpenConns)
	w.SetMaxIdleConns(dbConfig.MaxOpenConns)
	w.SetConnMaxIdleTime(time.Duration(120) * time.Second)
	w.SetConnMaxLifetime(time.Duration(dbConfig.ConnMaxLifeTime) * time.Second)

	// Ping verifies a connection to the database is still alive, establishing a connection if necessary.
	if err := w.Ping(); err != nil {
		return nil, err
	}
	if dbConfig.HostRead != "" {
		dbConfig.HostRead = ParseHost(dbConfig.HostRead)
		dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?%s",
			dbConfig.User,
			dbConfig.Password,
			dbConfig.HostRead,
			dbConfig.PortRead,
			dbConfig.Database,
			query.Encode())
		r, err := sql.Open(driverName, dsn)
		if err != nil {
			return nil, err
		}
		if dbConfig.MaxOpenReadConns == 0 {
			dbConfig.MaxOpenReadConns = 10
		}
		r.SetMaxOpenConns(dbConfig.MaxOpenReadConns)
		r.SetMaxIdleConns(dbConfig.MaxOpenReadConns)
		r.SetConnMaxIdleTime(time.Duration(120) * time.Second)
		r.SetConnMaxLifetime(time.Duration(dbConfig.ConnMaxLifeTime) * time.Second)
		return &DB{
			reader: r,
			writer: w,
		}, nil
	}

	return &DB{
		reader: w,
		writer: w,
	}, nil
}

// FOR UT
func (db *DB) Close() error {
	db.reader.Close()
	return db.writer.Close()
}
