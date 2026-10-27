// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package auth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/internal/model"
)

func TestCreateLocalUserConcurrentAccountConflict(t *testing.T) {
	s := newStore(t)
	pool, err := s.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	// Keep SQLite's in-memory database on one connection. Requests still race
	// through the store, while the database serializes competing inserts.
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	const requests = 4
	results := make(chan error, requests)
	start := make(chan struct{})
	for i := range requests {
		go func() {
			<-start
			results <- s.CreateLocalUser(t.Context(), &model.User{
				ID: fmt.Sprintf("concurrent-%d", i), Account: "same-account", Enabled: true,
			}, "initial-password")
		}()
	}
	close(start)
	successes := 0
	for range requests {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrAccountAlreadyExists) {
			t.Errorf("concurrent create: want account conflict, got %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful creates = %d, want 1", successes)
	}
	var count int64
	if err := s.db.Model(&model.User{}).Where("account = ?", "same-account").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("stored accounts = %d, err = %v; want 1", count, err)
	}
}

func TestCreateLocalUserPrimaryKeyConflictIsNotAccountConflict(t *testing.T) {
	s := newStore(t)
	if err := s.CreateLocalUser(t.Context(), &model.User{ID: "same-id", Account: "first"}, "password"); err != nil {
		t.Fatal(err)
	}
	err := s.CreateLocalUser(t.Context(), &model.User{ID: "same-id", Account: "second"}, "password")
	if err == nil || errors.Is(err, ErrAccountAlreadyExists) {
		t.Fatalf("primary key conflict misclassified: %v", err)
	}
}
