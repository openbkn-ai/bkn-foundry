// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"testing"
	"time"
)

func TestPolicyTransactionSerializesOrdinaryPolicyWrites(t *testing.T) {
	e := newTestEnforcer(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	transactionDone := make(chan error, 1)
	go func() {
		transactionDone <- e.Transaction(t.Context(), func(tx *PolicyTransaction) error {
			if err := tx.GrantObjectPermission("proxy", "resource", "r-1", "query_data"); err != nil {
				return err
			}
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	allowed, err := e.e.HasPolicy("proxy", obj("resource", "r-1"), "query_data")
	if err != nil || allowed {
		t.Fatalf("uncommitted policy was visible: allowed=%v err=%v", allowed, err)
	}

	ordinaryWriteDone := make(chan error, 1)
	go func() {
		ordinaryWriteDone <- e.GrantObjectPermission("user", "resource", "r-2", "view_detail")
	}()
	select {
	case err := <-ordinaryWriteDone:
		t.Fatalf("ordinary write escaped active policy transaction: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := waitForTestResult(t, transactionDone); err != nil {
		t.Fatalf("Transaction() error = %v", err)
	}
	if err := waitForTestResult(t, ordinaryWriteDone); err != nil {
		t.Fatalf("GrantObjectPermission() error = %v", err)
	}
	for _, check := range []struct {
		accessor, resource, operation string
	}{
		{"proxy", "r-1", "query_data"},
		{"user", "r-2", "view_detail"},
	} {
		allowed, err := e.e.HasPolicy(check.accessor, obj("resource", check.resource), check.operation)
		if err != nil || !allowed {
			t.Fatalf("Check(%q, %q, %q) = %v, %v", check.accessor, check.resource, check.operation, allowed, err)
		}
	}
}

func waitForTestResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for concurrent policy write")
		return nil
	}
}
