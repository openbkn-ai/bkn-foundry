// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package authz

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"strings"
	"time"

	"github.com/casbin/casbin/v2"
	casbinmodel "github.com/casbin/casbin/v2/model"
	"github.com/casbin/casbin/v2/persist"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"
)

// slowPolicyWrite is the duration past which a policy write is logged with its
// phase breakdown, so a queue building up behind the write slot shows in the
// logs before callers start timing out (#1511).
const slowPolicyWrite = time.Second

// ErrPolicyReloadAfterCommit means the database transaction committed but the
// live authorization cache could not be refreshed. Mutation callers receive an
// error and may retry, but must not audit the committed write as denied.
var ErrPolicyReloadAfterCommit = errors.New("reload committed casbin policy")

// PolicyTransaction is a Casbin transaction and its underlying GORM
// transaction. Domain services can persist provenance rows through DB while
// changing the matching policy through the methods below; both commit or roll
// back together, and a commit publishes the new policy to the live model.
//
// Inside a transaction, use only these methods. The Enforcer's own write entry
// points (Transaction, AssignRole, RemoveRole, ReloadPolicy) take the write
// slot the transaction already holds: they are not re-entrant, and the
// transactional enforcer has no slot, so they fail there with an error.
type PolicyTransaction struct {
	db       *gorm.DB
	enforcer *Enforcer
}

func (tx *PolicyTransaction) DB() *gorm.DB { return tx.db }

func (tx *PolicyTransaction) Check(accessorID, resourceType, resourceID, operation string) (bool, error) {
	return tx.enforcer.Check(accessorID, resourceType, resourceID, operation)
}

func (tx *PolicyTransaction) HasObjectPermission(accessorID, resourceType, resourceID, operation string) (bool, error) {
	rows, err := tx.enforcer.e.GetFilteredPolicy(0, accessorID, obj(resourceType, resourceID), operation, EffectAllow)
	if err != nil {
		return false, err
	}
	return len(activePolicyRows(rows)) > 0, nil
}

func (tx *PolicyTransaction) GrantPolicy(grant PolicyGrant) (bool, error) {
	return tx.enforcer.addPolicyGrant(grant)
}

func (tx *PolicyTransaction) RevokePolicy(grantID string) (bool, error) {
	removed, _, err := tx.enforcer.revokePolicyGrant(grantID)
	return removed, err
}

// GrantSeedPolicy and RemoveSeedRolePermissions let startup reconcile the
// complete built-in role matrix in two batch transactions rather than opening
// and reloading Casbin once per operation.
func (tx *PolicyTransaction) GrantSeedPolicy(roleID, object, operation string) error {
	return tx.enforcer.addPolicy(roleID, object, operation, EffectAllow,
		PolicySourceRolePermission, AuthoritySourceSystem)
}

func (tx *PolicyTransaction) RemoveSeedRolePermissions(roleID string) error {
	_, err := tx.enforcer.removePolicyGrants(PolicyFilter{
		AccessorID: roleID, PolicySource: PolicySourceRolePermission,
	})
	return err
}

// RemovePoliciesForResourceTypes removes every durable grant and its Casbin
// projection for the supplied resource-type prefixes. It is reserved for
// startup migrations that withdraw an entire resource type from the platform.
func (tx *PolicyTransaction) RemovePoliciesForResourceTypes(resourceTypes ...string) (int, error) {
	removed := 0
	for _, resourceType := range resourceTypes {
		count, err := tx.enforcer.removePolicyGrantsByObjectPrefix(resourceType + ":")
		if err != nil {
			return removed, err
		}
		removed += count
	}
	return removed, nil
}

// RemovePoliciesForOperation removes every durable grant and Casbin projection
// for one withdrawn operation while preserving the resource type's other
// grants. It is reserved for startup vocabulary migrations.
func (tx *PolicyTransaction) RemovePoliciesForOperation(resourceType, operation string) (int, error) {
	resourceType = strings.TrimSpace(resourceType)
	operation = strings.TrimSpace(operation)
	if resourceType == "" {
		return 0, errors.New("resource type is required")
	}
	if operation == "" {
		return 0, errors.New("operation is required")
	}
	return tx.enforcer.removePolicyGrantsByObjectPrefixAndOperation(resourceType+":", operation)
}

func (tx *PolicyTransaction) GrantObjectPermission(accessorID, resourceType, resourceID, operation string) error {
	return tx.enforcer.addPolicy(accessorID, obj(resourceType, resourceID), operation, EffectAllow,
		PolicySourceSystemDerived, AuthoritySourceSystem)
}

func (tx *PolicyTransaction) RevokeObjectPermission(accessorID, resourceType, resourceID, operation string) error {
	return tx.enforcer.removePolicy(accessorID, obj(resourceType, resourceID), operation, EffectAllow,
		PolicySourceSystemDerived, AuthoritySourceSystem)
}

// acquireWrite takes the single policy-write slot. It honours ctx while queued
// and checks it again once admitted: a caller that has already given up must
// not start a transaction whose result nobody will read.
func (en *Enforcer) acquireWrite(ctx context.Context) (func(), error) {
	if en.writeSlot == nil {
		return nil, errors.New("authz enforcer has no policy write slot")
	}
	select {
	case en.writeSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-en.writeSlot
		return nil, err
	}
	return func() { <-en.writeSlot }, nil
}

// Transaction runs fn against a transactional copy of the live policy model,
// with policy rows and provenance rows sharing one database transaction.
//
// Every write holds the write slot, so the live model equals the committed
// store when the slot is acquired. The transaction therefore starts from an
// in-memory copy instead of a reload, and after the commit that copy — which
// matches the store again — replaces the live model in one step: a concurrent
// check sees all of the write or none of it, never an uncommitted allow.
// Before #1511 every write reloaded the whole store three times, so one write
// cost O(rules) and concurrent writers queued past their callers' deadlines.
func (en *Enforcer) Transaction(ctx context.Context, fn func(*PolicyTransaction) error) error {
	queued := time.Now()
	release, err := en.acquireWrite(ctx)
	if err != nil {
		return err
	}
	defer release()
	admitted := time.Now()

	live, ok := en.e.(*casbin.SyncedEnforcer)
	if !ok {
		return fmt.Errorf("authz enforcer %T does not support policy transactions", en.e)
	}
	lock := live.GetLock()
	lock.RLock()
	snapshot := live.Enforcer.GetModel().Copy()
	lock.RUnlock()
	// An enforcer built from a model alone never loads from an adapter; the
	// transaction-bound adapter is attached once the transaction exists.
	txEnforcer, err := casbin.NewEnforcer(snapshot)
	if err != nil {
		return fmt.Errorf("create transactional casbin enforcer: %w", err)
	}
	if err := txEnforcer.BuildRoleLinks(); err != nil {
		return fmt.Errorf("build transactional role links: %w", err)
	}
	copied := time.Now()

	recorder := &recordingAdapter{inserted: map[string][][]string{}}
	err = en.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// No auto-migration here: DDL inside a MySQL transaction commits it.
		adapterDB := tx.Session(&gorm.Session{NewDB: true})
		gormadapter.TurnOffAutoMigrate(adapterDB)
		txAdapter, err := gormadapter.NewAdapterByDBUseTableName(adapterDB, "", "casbin_rule")
		if err != nil {
			return fmt.Errorf("bind casbin adapter to transaction: %w", err)
		}
		recorder.Adapter = txAdapter
		txEnforcer.SetAdapter(recorder)
		// Domain models resolve their own table names from a clean handle that
		// shares the transaction's connection, and so its commit boundary.
		txDB := en.db.Session(&gorm.Session{NewDB: true, Context: ctx})
		txDB.Statement.ConnPool = tx.Statement.ConnPool
		return fn(&PolicyTransaction{
			db:       txDB,
			enforcer: &Enforcer{e: txEnforcer, db: txDB},
		})
	})
	if err != nil {
		return err
	}
	committed := time.Now()
	publishErr := en.publish(live, snapshot, recorder)
	if took := time.Since(queued); took >= slowPolicyWrite {
		slog.Warn("slow authorization policy write",
			"took", took, "queued", admitted.Sub(queued), "copy", copied.Sub(admitted),
			"transaction", committed.Sub(copied), "publish", time.Since(committed),
			"rules", len(snapshot["p"]["p"].Policy))
	}
	return publishErr
}

// publish makes a committed snapshot the live model. Inserts leave it in the
// store's row order; a removal does not (casbin moves the last rule into the
// freed slot), so it is re-ordered first — listings such as GET
// /authz/policies return rules in that order. Should the snapshot fail to
// reconcile or install, the live model is rebuilt from the store instead.
//
// The model is read and replaced through the embedded, unsynchronized
// *casbin.Enforcer on purpose: the caller holds the write slot and this
// function takes the SyncedEnforcer lock itself, so a synchronized variant
// would lock twice.
func (en *Enforcer) publish(live *casbin.SyncedEnforcer, snapshot casbinmodel.Model, recorder *recordingAdapter) error {
	if !recorder.removed || canonicalizeOrder(live.Enforcer.GetModel(), snapshot, recorder.inserted) { //nolint:staticcheck // explicit unsynchronized call, see above
		lock := live.GetLock()
		lock.Lock()
		live.Enforcer.SetModel(snapshot) //nolint:staticcheck // explicit unsynchronized call under the held lock
		err := live.Enforcer.BuildRoleLinks()
		lock.Unlock()
		if err == nil {
			return nil
		}
		slog.Error("installing committed authorization policy failed; reloading from the store", "err", err)
	} else {
		slog.Error("committed authorization policy did not reconcile with the live model; reloading from the store")
	}
	if err := live.LoadPolicy(); err != nil {
		return fmt.Errorf("%w: %v", ErrPolicyReloadAfterCommit, err)
	}
	return nil
}

// ReloadPolicy rebuilds the live model from the store. It holds the write slot
// so a reload can never replace a newer published write with an older read,
// and reports whether the store differed from what was live.
func (en *Enforcer) ReloadPolicy(ctx context.Context) (bool, error) {
	release, err := en.acquireWrite(ctx)
	if err != nil {
		return false, err
	}
	defer release()
	before := en.policyFingerprint()
	if err := en.e.LoadPolicy(); err != nil {
		return false, err
	}
	return en.policyFingerprint() != before, nil
}

// policyDigest is an order-independent digest of the live p and g rules.
type policyDigest struct {
	rules int
	sum   uint64
}

func (en *Enforcer) policyFingerprint() policyDigest {
	var fp policyDigest
	live, ok := en.e.(*casbin.SyncedEnforcer)
	if !ok {
		return fp
	}
	lock := live.GetLock()
	lock.RLock()
	defer lock.RUnlock()
	for _, sec := range []string{"p", "g"} {
		for ptype, ast := range live.Enforcer.GetModel()[sec] { //nolint:staticcheck // explicit unsynchronized call under the held read lock
			for key := range ast.PolicyMap {
				h := fnv.New64a()
				_, _ = h.Write([]byte(sec + "\x00" + ptype + "\x00" + key))
				fp.sum += h.Sum64()
				fp.rules++
			}
		}
	}
	return fp
}

// recordingAdapter forwards policy writes to the transaction-bound adapter and
// remembers which rows were inserted, in order, so the published model can
// reproduce the store's row order.
type recordingAdapter struct {
	persist.Adapter
	inserted map[string][][]string // "sec.ptype" -> rules in insert order
	removed  bool
}

func (a *recordingAdapter) recordInserted(sec, ptype string, rules ...[]string) {
	for _, rule := range rules {
		a.inserted[sec+"."+ptype] = append(a.inserted[sec+"."+ptype], append([]string(nil), rule...))
	}
}

func (a *recordingAdapter) AddPolicy(sec, ptype string, rule []string) error {
	if err := a.Adapter.AddPolicy(sec, ptype, rule); err != nil {
		return err
	}
	a.recordInserted(sec, ptype, rule)
	return nil
}

func (a *recordingAdapter) RemovePolicy(sec, ptype string, rule []string) error {
	a.removed = true
	return a.Adapter.RemovePolicy(sec, ptype, rule)
}

func (a *recordingAdapter) RemoveFilteredPolicy(sec, ptype string, fieldIndex int, fieldValues ...string) error {
	a.removed = true
	return a.Adapter.RemoveFilteredPolicy(sec, ptype, fieldIndex, fieldValues...)
}

// AddPolicies and RemovePolicies are part of persist.BatchAdapter, which casbin
// asserts without checking; they must exist on the wrapper.
func (a *recordingAdapter) AddPolicies(sec, ptype string, rules [][]string) error {
	batch, ok := a.Adapter.(persist.BatchAdapter)
	if !ok {
		return errors.New("transaction adapter does not support batch writes")
	}
	if err := batch.AddPolicies(sec, ptype, rules); err != nil {
		return err
	}
	a.recordInserted(sec, ptype, rules...)
	return nil
}

func (a *recordingAdapter) RemovePolicies(sec, ptype string, rules [][]string) error {
	batch, ok := a.Adapter.(persist.BatchAdapter)
	if !ok {
		return errors.New("transaction adapter does not support batch writes")
	}
	a.removed = true
	return batch.RemovePolicies(sec, ptype, rules)
}

// Whole-store saves and in-place updates would defeat the row-order
// bookkeeping above. Nothing in bkn-safe issues them inside a transaction, so
// they are refused rather than published in the wrong order.
func (a *recordingAdapter) SavePolicy(casbinmodel.Model) error {
	return errors.New("saving the whole policy is not supported in authz transactions")
}

func (a *recordingAdapter) UpdatePolicy(string, string, []string, []string) error {
	return errors.New("policy updates are not supported in authz transactions")
}

func (a *recordingAdapter) UpdatePolicies(string, string, [][]string, [][]string) error {
	return errors.New("policy updates are not supported in authz transactions")
}

func (a *recordingAdapter) UpdateFilteredPolicies(string, string, [][]string, int, ...string) ([][]string, error) {
	return nil, errors.New("policy updates are not supported in authz transactions")
}

// canonicalizeOrder rewrites next's rule order to what a reload of the store
// would produce: rules of base that survived keep their position, and inserted
// rules follow in the order of their last insert (a re-inserted rule gets a new
// row id). It reports false if the two do not reconcile.
func canonicalizeOrder(base, next casbinmodel.Model, inserted map[string][][]string) bool {
	for sec, assertions := range next {
		if sec != "p" && sec != "g" {
			continue
		}
		for ptype, ast := range assertions {
			added := inserted[sec+"."+ptype]
			lastInsert := make(map[string]int, len(added))
			for i, rule := range added {
				lastInsert[strings.Join(rule, casbinmodel.DefaultSep)] = i
			}
			ordered := make([][]string, 0, len(ast.Policy))
			keys := make([]string, 0, len(ast.Policy))
			if baseAst, ok := base[sec][ptype]; ok {
				baseKeys := make([]string, len(baseAst.Policy))
				for key, idx := range baseAst.PolicyMap {
					baseKeys[idx] = key
				}
				for _, key := range baseKeys {
					if _, reinserted := lastInsert[key]; reinserted {
						continue
					}
					if idx, ok := ast.PolicyMap[key]; ok {
						ordered = append(ordered, ast.Policy[idx])
						keys = append(keys, key)
					}
				}
			}
			for i, rule := range added {
				key := strings.Join(rule, casbinmodel.DefaultSep)
				if lastInsert[key] != i {
					continue
				}
				if idx, ok := ast.PolicyMap[key]; ok {
					ordered = append(ordered, ast.Policy[idx])
					keys = append(keys, key)
				}
			}
			if len(ordered) != len(ast.Policy) {
				return false
			}
			policyMap := make(map[string]int, len(keys))
			for i, key := range keys {
				policyMap[key] = i
			}
			ast.Policy, ast.PolicyMap = ordered, policyMap
		}
	}
	return true
}
