// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"
)

// ErrPolicyReloadAfterCommit means the database transaction committed but the
// live authorization cache could not be refreshed. Mutation callers receive an
// error and may retry, but must not audit the committed write as denied.
var ErrPolicyReloadAfterCommit = errors.New("reload committed casbin policy")

// PolicyTransaction is a Casbin transaction and its underlying GORM
// transaction. Domain services can persist provenance rows through DB while
// changing the matching policy through the methods below; the adapter commits
// or rolls both back together and reloads the live in-memory policy.
type PolicyTransaction struct {
	db       *gorm.DB
	enforcer *Enforcer
}

func (tx *PolicyTransaction) DB() *gorm.DB { return tx.db }

func (tx *PolicyTransaction) Check(accessorID, resourceType, resourceID, operation string) (bool, error) {
	return tx.enforcer.Check(accessorID, resourceType, resourceID, operation)
}

func (tx *PolicyTransaction) HasObjectPermission(accessorID, resourceType, resourceID, operation string) (bool, error) {
	return tx.enforcer.e.HasPolicy(accessorID, obj(resourceType, resourceID), operation)
}

func (tx *PolicyTransaction) GrantObjectPermission(accessorID, resourceType, resourceID, operation string) error {
	_, err := tx.enforcer.e.AddPolicy(accessorID, obj(resourceType, resourceID), operation)
	return err
}

func (tx *PolicyTransaction) RevokeObjectPermission(accessorID, resourceType, resourceID, operation string) error {
	_, err := tx.enforcer.e.RemovePolicy(accessorID, obj(resourceType, resourceID), operation)
	return err
}

// Transaction runs fn inside the gorm-adapter transaction used by Casbin. The
// adapter temporarily points the callback enforcer at the transaction-bound DB;
// deriving tx.db from that adapter is what gives relational rows and policy rows
// one commit boundary.
func (en *Enforcer) Transaction(ctx context.Context, fn func(*PolicyTransaction) error) error {
	en.transactionMu.Lock()
	defer en.transactionMu.Unlock()

	adapter, ok := en.e.GetAdapter().(*gormadapter.Adapter)
	if !ok {
		return fmt.Errorf("authz adapter does not support gorm transactions")
	}
	// Do not let the adapter swap the live enforcer onto its transaction-bound
	// DB: AddPolicy mutates the in-memory model before commit, which would expose
	// an uncommitted allow to concurrent authorization checks. A short-lived
	// enforcer owns the transactional model; the live model reloads only after a
	// successful commit.
	m, err := casbinmodel.NewModelFromString(modelConf)
	if err != nil {
		return fmt.Errorf("parse transactional casbin model: %w", err)
	}
	transactionEnforcer, err := casbin.NewEnforcer(m, adapter)
	if err != nil {
		return fmt.Errorf("create transactional casbin enforcer: %w", err)
	}
	err = adapter.Transaction(transactionEnforcer, func(txEnforcer casbin.IEnforcer) error {
		base, ok := txEnforcer.(*casbin.Enforcer)
		if !ok {
			return fmt.Errorf("unexpected transactional enforcer type %T", txEnforcer)
		}
		txAdapter, ok := txEnforcer.GetAdapter().(*gormadapter.Adapter)
		if !ok {
			return fmt.Errorf("unexpected transactional adapter type %T", txEnforcer.GetAdapter())
		}
		// GetDb inherits a permanent casbin_rule table scope. Start from the
		// application's clean GORM handle, then attach the adapter transaction's
		// connection pool so domain models resolve their own table names while
		// still sharing the exact same commit boundary.
		txDB := en.db.Session(&gorm.Session{NewDB: true, Context: ctx})
		txDB.Statement.ConnPool = txAdapter.GetDb().Statement.ConnPool
		return fn(&PolicyTransaction{
			db:       txDB,
			enforcer: &Enforcer{e: base, db: txDB},
		})
	})
	if err != nil {
		return err
	}
	if err := en.e.LoadPolicy(); err != nil {
		return fmt.Errorf("%w: %v", ErrPolicyReloadAfterCommit, err)
	}
	return nil
}
