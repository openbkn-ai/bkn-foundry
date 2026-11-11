//go:build integration

// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func actionOperationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("BKN_MIGRATION_TEST_DB_HOST")
	if host == "" {
		t.Skip("requires an explicit MariaDB test connection")
	}
	port := os.Getenv("BKN_MIGRATION_TEST_DB_PORT")
	if port == "" {
		port = "3306"
	}
	user := os.Getenv("BKN_MIGRATION_TEST_DB_USER")
	if user == "" {
		user = "root"
	}
	config := mysql.NewConfig()
	config.User, config.Passwd = user, os.Getenv("BKN_MIGRATION_TEST_DB_PASSWORD")
	config.Net, config.Addr = "tcp", net.JoinHostPort(host, port)
	admin, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	var version string
	if err := admin.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("storage integration database version: %s", version)
	var major, minor int
	if n, err := fmt.Sscanf(version, "%d.%d", &major, &minor); err != nil || n != 2 || !strings.Contains(version, "MariaDB") || major < 11 || (major == 11 && minor < 4) {
		t.Fatalf("requires MariaDB 11.4 or newer, got %q", version)
	}
	database := fmt.Sprintf("action_storage_it_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE `" + database + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP DATABASE `" + database + "`"); err != nil {
			t.Error(err)
		}
	})
	config.DBName = database
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, file, _, _ := runtime.Caller(0)
	initialization, err := os.ReadFile(filepath.Join(filepath.Dir(file), "../../../../../../migrations/bkn-backend/mariadb/0.2.2/init.sql"))
	if err != nil {
		t.Fatal(err)
	}
	ddl := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS t_action_type \(.*?;`).Find(initialization)
	if len(ddl) == 0 {
		t.Fatal("action schema is missing from initialization")
	}
	if _, err := db.Exec(string(ddl)); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestActionOperationMariaDBStorageRoundTrip(t *testing.T) {
	db := actionOperationTestDB(t)
	access := &actionTypeAccess{db: db}
	ctx := context.Background()
	tool := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ATID: "tool", ATName: "Tool", ActionType: "modify", ActionIntent: "modify",
		OperationConfig: &interfaces.ActionOperationConfig{Type: "tool"},
		ActionSource:    interfaces.ActionSource{Type: "tool", BoxID: "box", ToolID: "call"},
		Parameters:      []interfaces.Parameter{{Name: "reference", Type: "integer", ValueFrom: "const", Value: json.Number("9223372036854775807")}},
	}, KNID: "kn", Branch: "main"}
	tool.Comment = "Storage integration fixture"
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := access.CreateActionType(ctx, tx, tool); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	loaded, err := access.GetActionTypesByIDs(ctx, "kn", "main", []string{"tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || !reflect.DeepEqual(tool.OperationConfig, loaded[0].OperationConfig) || fmt.Sprint(loaded[0].Parameters[0].Value) != "9223372036854775807" {
		t.Fatalf("lost tool metadata or numeric literal: %#v", loaded)
	}
	relations := []*interfaces.ActionType{}
	for _, id := range []string{"link", "unlink-a", "unlink-b"} {
		operation := "delete"
		if id == "link" {
			operation = "create"
		}
		relation := &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
			ATID: id, ATName: id, ActionType: "add", ActionIntent: "add", ObjectTypeID: "orders",
			OperationConfig: &interfaces.ActionOperationConfig{Type: "relation", RelationTypeID: "suppliers", RelationOperation: operation, AnchorRole: "target", OtherObjectCondition: &interfaces.ActionCondCfg{Field: "status", Operation: "==", ValueFrom: "const", Value: "active"}},
			ActionSource:    interfaces.ActionSource{Type: "mcp", McpID: "mcp", ToolName: "link"},
			Parameters:      []interfaces.Parameter{{Name: "source", Type: "string", ValueFrom: "source_identity"}, {Name: "target", Type: "string", ValueFrom: "target_identity"}},
		}, KNID: "kn", Branch: "main"}
		relation.Comment = "Relation storage fixture"
		relations = append(relations, relation)
	}
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := access.CreateActionTypes(ctx, tx, relations); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	query := interfaces.ActionTypesQueryParams{KNID: "kn", Branch: "main", OperationKind: "relation", RelationOperation: "delete"}
	query.Sort, query.Direction, query.Limit, query.Offset = "f_name", "asc", 1, 1
	total, err := access.GetActionTypesTotal(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("filtered total=%d, want 2", total)
	}
	page, err := access.ListActionTypes(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := access.ListActionTypeSummaries(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	for _, entries := range [][]*interfaces.ActionType{page, summary} {
		if len(entries) != 1 || entries[0].ATID != "unlink-b" || !reflect.DeepEqual(relations[2].OperationConfig, entries[0].OperationConfig) {
			t.Fatalf("incorrect filtered page or relation metadata: %#v", entries)
		}
	}
	relations[1].OperationConfig.RelationOperation = "create"
	relations[2].OperationConfig.AnchorRole = "source"
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := access.UpdateActionTypes(ctx, tx, relations[1:]); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	total, err = access.GetActionTypesTotal(ctx, query)
	if err != nil || total != 1 {
		t.Fatalf("updated filtered total=%d err=%v", total, err)
	}
	loaded, err = access.GetActionTypesByIDs(ctx, "kn", "main", []string{"unlink-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].OperationConfig.AnchorRole != "source" {
		t.Fatal("batch update lost relation endpoint role")
	}
	tool.Parameters[0].Value = json.Number("9223372036854775806")
	tx, err = db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := access.UpdateActionType(ctx, tx, tool); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	loaded, err = access.GetActionTypesByIDs(ctx, "kn", "main", []string{"tool"})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || fmt.Sprint(loaded[0].Parameters[0].Value) != "9223372036854775806" {
		t.Fatal("single update rounded a typed constant")
	}
}
