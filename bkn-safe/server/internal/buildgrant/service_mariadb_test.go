//go:build integration

// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package buildgrant

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	driver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func mariaDB(t *testing.T) *gorm.DB {
	t.Helper()
	raw := os.Getenv("SAFE_BUILD_TEST_DSN")
	if raw == "" {
		t.Skip("SAFE_BUILD_TEST_DSN is unset")
	}
	cfg, err := driver.ParseDSN(raw)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if !strings.HasPrefix(cfg.Addr, "127.0.0.1:") {
		t.Fatal("test database must be local")
	}
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal("open test server")
	}
	name := "safe_test_build_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal("create test database")
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE " + name); admin.Close() })
	cfg.DBName = name
	cfg.ParseTime = true
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("open isolated test database")
	}
	if err = db.AutoMigrate(&model.User{}, &model.ManagedProxyAccount{}); err != nil {
		t.Fatal("migrate isolated build tables")
	}
	migration, err := os.ReadFile("../../migrations/20261101-build-grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{}
	for _, line := range strings.Split(string(migration), "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "--") {
			lines = append(lines, line)
		}
	}
	script := strings.Join(lines, "\n")
	for run := 0; run < 2; run++ {
		for _, statement := range strings.Split(strings.ReplaceAll(script, "USE openbkn;", "USE "+name+";"), ";") {
			if strings.TrimSpace(statement) != "" {
				if err = db.Exec(statement).Error; err != nil {
					t.Fatal("apply manual build schema", err)
				}
			}
		}
	}

	if err = db.Create(&model.User{ID: "owner", Account: "owner", Enabled: true, AccountType: model.AccountTypeOther}).Error; err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { conn.Close() })
	return db
}

type alwaysAllow struct{}

func (alwaysAllow) CheckContext(context.Context, string, string, string, string) (bool, error) {
	return true, nil
}
func TestMariaDBBuildGrantRaces(t *testing.T) {
	db := mariaDB(t)
	s := New(db, alwaysAllow{})
	request := func(id string) CreateRequest {
		return CreateRequest{ID: "grant-" + id, ProjectID: "project", BuildID: "build-" + id, DelegatorID: "owner", ModelFingerprint: strings.Repeat("a", 64), Versions: []Version{{KnowledgeBaseID: "1", FileID: "41", ContentVersionID: "8"}}}
	}
	for i := 0; i < 12; i++ {
		r := request(uuid.NewString())
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := s.Create(t.Context(), r)
			if errors.Is(err, ErrConflict) {
				err = nil
			}
			results <- err
		}()
		go func() { defer wg.Done(); <-start; results <- s.Revoke(t.Context(), r.ID, r.ProjectID, r.BuildID) }()
		close(start)
		wg.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal("create/revoke race failed", err)
			}
		}
		if _, _, err := s.Create(t.Context(), r); !errors.Is(err, ErrConflict) {
			t.Fatal("late create bypassed cancellation", err)
		}
	}
	r := request("duplicate")
	start := make(chan struct{})
	type result struct {
		g       *Grant
		created bool
		err     error
	}
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() { <-start; g, created, err := s.Create(t.Context(), r); results <- result{g, created, err} }()
	}
	close(start)
	a, b := <-results, <-results
	if a.err != nil || b.err != nil || a.g.PrincipalID != b.g.PrincipalID || a.created == b.created {
		t.Fatal("concurrent replay created multiple identities", a.err, b.err)
	}
	var orphaned int64
	if err := db.Model(&model.User{}).Where("id <> ? AND NOT EXISTS (SELECT 1 FROM managed_proxy_accounts m WHERE m.proxy_account_id=users.id)", "owner").Count(&orphaned).Error; err != nil || orphaned != 0 {
		t.Fatal("orphaned build identity", err)
	}
}
