// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"errors"
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
	allowed, err := e.e.HasPolicy("proxy", obj("resource", "r-1"), "query_data", EffectAllow,
		string(PolicySourceSystemDerived), string(AuthoritySourceSystem))
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
		source                        PolicySource
		authority                     AuthoritySource
	}{
		{"proxy", "r-1", "query_data", PolicySourceSystemDerived, AuthoritySourceSystem},
		{"user", "r-2", "view_detail", PolicySourceLegacy, AuthoritySourceMigration},
	} {
		allowed, err := e.e.HasPolicy(check.accessor, obj("resource", check.resource), check.operation, EffectAllow,
			string(check.source), string(check.authority))
		if err != nil || !allowed {
			t.Fatalf("Check(%q, %q, %q) = %v, %v", check.accessor, check.resource, check.operation, allowed, err)
		}
	}
}

func TestPolicyTransactionRollsBackGrantIdentityAndProjectionTogether(t *testing.T) {
	e, db := newTestEnforcerDB(t)
	rollback := errors.New("force rollback")
	grant := PolicyGrant{
		GrantID: "grant-rollback", AccessorID: "u-1", Object: "resource:r-1", Operation: "view_detail",
		Effect: EffectAllow, PolicySource: PolicySourceProfessionalRule,
		AuthoritySource: AuthoritySourceAdminAuthz, CreatedBy: "admin-1",
	}
	err := e.Transaction(t.Context(), func(tx *PolicyTransaction) error {
		created, err := tx.GrantPolicy(grant)
		if err != nil || !created {
			t.Fatalf("transactional GrantPolicy() = %v, %v; want created", created, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("Transaction() error = %v, want rollback", err)
	}
	var grants, projections int64
	if err := db.Table("authorization_grant").Where("grant_id = ?", grant.GrantID).Count(&grants).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("casbin_rule").Where("ptype = ? AND v0 = ? AND v1 = ?", "p", grant.AccessorID, grant.Object).
		Count(&projections).Error; err != nil {
		t.Fatal(err)
	}
	if grants != 0 || projections != 0 {
		t.Fatalf("rolled-back state: grants=%d projections=%d; want both zero", grants, projections)
	}
	allowed, checkErr := e.Check("u-1", "resource", "r-1", "view_detail")
	if checkErr != nil || allowed {
		t.Fatalf("rolled-back grant visible: allowed=%v err=%v", allowed, checkErr)
	}
}

func TestRemovePoliciesForOperationRejectsEmptyTargetsWithoutDeleting(t *testing.T) {
	e := newTestEnforcer(t)
	const (
		accessorID   = "connector-operator"
		resourceID   = "remote-api"
		resourceType = "connector_type"
	)
	for _, operation := range []string{"modify", "task_manage"} {
		if err := e.GrantObjectPermission(accessorID, resourceType, resourceID, operation); err != nil {
			t.Fatal(err)
		}
	}

	for _, tc := range []struct {
		name         string
		resourceType string
		operation    string
	}{
		{name: "empty resource type", operation: "task_manage"},
		{name: "blank resource type", resourceType: " \t", operation: "task_manage"},
		{name: "empty operation", resourceType: resourceType},
		{name: "blank operation", resourceType: resourceType, operation: " \t"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := e.Transaction(t.Context(), func(tx *PolicyTransaction) error {
				_, err := tx.RemovePoliciesForOperation(tc.resourceType, tc.operation)
				return err
			})
			if err == nil {
				t.Fatal("RemovePoliciesForOperation() error = nil; want validation error")
			}
			for _, operation := range []string{"modify", "task_manage"} {
				allowed, checkErr := e.Check(accessorID, resourceType, resourceID, operation)
				if checkErr != nil || !allowed {
					t.Fatalf("grant %s was removed after rejected migration: allowed=%v err=%v", operation, allowed, checkErr)
				}
			}
		})
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
