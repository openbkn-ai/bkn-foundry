// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package kn_version

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

type permitAll struct{ interfaces.PermissionService }

func (permitAll) CheckPermission(context.Context, interfaces.PermissionResource, []string) error {
	return nil
}

// storedAccounts returns version rows as they are stored: account ids and types, no names.
type storedAccounts struct{ interfaces.KNVersionAccess }

func (storedAccounts) ListVersions(context.Context, string, *interfaces.KNVersionsQuery) ([]*interfaces.KNVersion, int, error) {
	return []*interfaces.KNVersion{
		{Version: 2, Publisher: interfaces.AccountInfo{ID: "u1", Type: "user"}, StatusBy: interfaces.AccountInfo{ID: "a1", Type: "app"}},
		{Version: 1},
	}, 2, nil
}

func (storedAccounts) GetVersion(context.Context, *sql.Tx, string, int, bool) (*interfaces.KNVersion, error) {
	return &interfaces.KNVersion{Version: 2, Publisher: interfaces.AccountInfo{ID: "u1", Type: "user"},
		StatusBy: interfaces.AccountInfo{ID: "u1", Type: "user"}}, nil
}

func (storedAccounts) ListEvents(context.Context, string, int, int) ([]*interfaces.KNVersionEvent, int, error) {
	return []*interfaces.KNVersionEvent{{Type: "publish", Operator: interfaces.AccountInfo{ID: "u1", Type: "user"}}}, 1, nil
}

// directory names accounts the way bkn-safe does, or fails when err is set.
type directory struct {
	err   error
	calls int
}

func (d *directory) GetAccountNames(_ context.Context, accounts []*interfaces.AccountInfo) error {
	d.calls++
	if d.err != nil {
		return d.err
	}
	names := map[string]string{"u1": "Alice", "a1": "etl-app"}
	for _, a := range accounts {
		if a.Type != "" {
			a.Name = names[a.ID]
		}
	}
	return nil
}

func newNamingService(dir *directory) *service {
	s := &service{va: storedAccounts{}, ps: permitAll{}}
	if dir != nil {
		s.ums = dir
	}
	return s
}

func TestVersionReadsNameAccounts(t *testing.T) {
	ctx := context.Background()
	s := newNamingService(&directory{})

	versions, _, err := s.ListVersions(ctx, "kn1", &interfaces.KNVersionsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if got := versions[0].Publisher.Name; got != "Alice" {
		t.Errorf("list publisher name = %q, want Alice", got)
	}
	if got := versions[0].StatusBy.Name; got != "etl-app" {
		t.Errorf("list status_by name = %q, want etl-app", got)
	}
	if got := versions[1].Publisher.Name; got != "" {
		t.Errorf("a version without a publisher got name %q", got)
	}

	version, err := s.GetVersion(ctx, "kn1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if version.Publisher.Name != "Alice" || version.StatusBy.Name != "Alice" {
		t.Errorf("detail names = %q/%q, want Alice/Alice", version.Publisher.Name, version.StatusBy.Name)
	}

	events, _, err := s.ListEvents(ctx, "kn1", 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	if got := events[0].Operator.Name; got != "Alice" {
		t.Errorf("event operator name = %q, want Alice", got)
	}
}

// A directory outage must not hide the version history: the reads succeed with ids only.
func TestVersionReadsKeepIDsWhenNamingFails(t *testing.T) {
	ctx := context.Background()
	dir := &directory{err: errors.New("bkn-safe unavailable")}
	s := newNamingService(dir)

	versions, _, err := s.ListVersions(ctx, "kn1", &interfaces.KNVersionsQuery{})
	if err != nil {
		t.Fatalf("list failed on a naming error: %v", err)
	}
	if versions[0].Publisher.ID != "u1" || versions[0].Publisher.Name != "" {
		t.Errorf("publisher = %+v, want id u1 without a name", versions[0].Publisher)
	}
	if _, err := s.GetVersion(ctx, "kn1", 2); err != nil {
		t.Fatalf("detail failed on a naming error: %v", err)
	}
	if _, _, err := s.ListEvents(ctx, "kn1", 0, 20); err != nil {
		t.Fatalf("events failed on a naming error: %v", err)
	}
	if dir.calls != 3 {
		t.Errorf("directory called %d times, want 3", dir.calls)
	}
}
