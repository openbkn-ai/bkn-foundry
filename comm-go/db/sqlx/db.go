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
go database/sql 连接池参数有 MaxIdleConns、MaxOpenConns：
MaxOpenConns 为最大连接数，MaxIdleConns 为最大空闲连接数，当一个连接用完之后，连接池会根据当前空闲连接个数
与 MaxIdleConns 的关系决定是关闭连接还是转为空闲连接，MaxIdleConns 默认是 2，如果 MaxOpenConns >> MaxIdleConns，
当数据库请求很高时，在大量创建连接后由于无法转为空闲连接，此时就会关闭连接，新请求再重新创建连接，造成大量的TIME_WAIT，
除了造成资源浪费，还会耗尽端口资源，造成无法创建新连接（连接发起方每次都使用随机端口，范围为 net.ipv4.ip_local_port_range，
默认 32768 - 60999）。为了避免此问题的发生，原则上应该设置为 MaxIdleConns = MaxOpenConns，但是并不是所有的服务都会一直
高频访问数据库，难免造成资源的浪费。

连接池还提供了 ConnMaxIdleTime、ConnMaxLifetime：
ConnMaxIdleTime 为空闲连接保持的最大时间，如果一个连接的空闲时间超过这个值，会被自动关闭，释放资源。
ConnMaxLifetime 为一个连接的最大存活时间，当超过这个时间，连接会被自动关闭，释放资源。（网上有文章提到此时间必须要比
数据库服务端wait_timeout小，仔细推敲此说法不对，应该是 ConnMaxIdleTime 必须小于数据库服务端wait_timeout）。
一般通过设置 ConnMaxIdleTime 来关闭长时间不使用的连接来释放资源，ConnMaxLifetime 可以用来控制1条SQL执行的最长时间，
避免服务器忙时放弃某些不重要的业务，但是如果数据库去掉提供了timeout、readTimeout、writeTimeout的功能，ConnMaxLifetime
无用武之地了。

综上：go 数据库标准如下：
1. MaxIdleConns == MaxOpenConns
2. ConnMaxIdleTime = 120s
120s是为了保证上一批关闭的处于 TIME_WAIT 状态的连接被内核回收。
TIME_WAIT 最长保留时间：https://blog.csdn.net/li1669852599/article/details/109629917
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

// ParseHost 判定host是否为IPv6格式，如果是，返回 [host]
func ParseHost(host string) string {
	if strings.Contains(host, ":") {
		return fmt.Sprintf("[%s]", host)
	}

	return host
}

// NewDB 新建数据库连接
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
