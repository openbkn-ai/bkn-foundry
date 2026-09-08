// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package capabilityindex keeps the Function tools in the capability index in step with the tool
// table.
//
// Every tool write syncs its own rows immediately (see SyncTools). This reconciler is the safety
// net underneath that: it catches a write whose transaction rolled back after the index was
// already told, a future write path nobody remembered to hook, and any row an immediate sync
// failed to deliver. It runs rarely for that reason — it is not the freshness mechanism.
//
// Skills and MCP tools are elsewhere. A Skill is written at the Skill index sync, the single choke
// point every Skill write already passes through, using the same rule for which snapshot counts.
// MCP tools live in logics/mcp, next to the client that can list them: their truth is on a remote
// server, not in a table here.
package capabilityindex

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/dbaccess"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/capability"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// Reconciler keeps the Function capabilities in the index matching the tool table.
type Reconciler interface {
	// Reconcile runs one full pass over every tool box.
	Reconcile(ctx context.Context) error
	// Start runs a pass now and then every interval, until the context is cancelled.
	Start(ctx context.Context, interval time.Duration)
	// SyncTools makes the index match the named tools as the table currently has them.
	SyncTools(ctx context.Context, boxID string, toolIDs []string) error
	// SyncBox makes the index match every tool of one box.
	SyncBox(ctx context.Context, boxID string) error
	// ForgetBox removes every tool of one box from the index.
	ForgetBox(ctx context.Context, boxID string) error
	// SyncToolsAsync runs SyncTools off the request path, logging what it cannot do.
	SyncToolsAsync(ctx context.Context, boxID string, toolIDs []string)
	// SyncBoxAsync runs SyncBox off the request path.
	SyncBoxAsync(ctx context.Context, boxID string)
	// ForgetBoxAsync runs ForgetBox off the request path.
	ForgetBoxAsync(ctx context.Context, boxID string)
}

type reconciler struct {
	logger    interfaces.Logger
	indexSync interfaces.CapabilityIndexSyncService
	toolRepo  model.IToolDB
	// running keeps two passes from overlapping: a pass reads every tool box, and a slow one must
	// not have the next tick start a second walk on top of it.
	running sync.Mutex
}

var (
	once     sync.Once
	instance Reconciler
)

// NewReconciler returns the capability index reconciler singleton.
func NewReconciler() Reconciler {
	once.Do(func() {
		conf := config.NewConfigLoader()
		instance = &reconciler{
			logger:    conf.GetLogger(),
			indexSync: capability.NewCapabilityIndexSyncService(),
			toolRepo:  dbaccess.NewToolDB(),
		}
	})
	return instance
}

// Start runs a pass immediately and then on every tick.
func (r *reconciler) Start(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		r.logger.Warn("capability index reconciler disabled: interval is not positive")
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			if err := r.Reconcile(ctx); err != nil {
				r.logger.Warnf("capability index reconcile failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Reconcile runs one pass over every tool box.
func (r *reconciler) Reconcile(ctx context.Context) (err error) {
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer func() { oteltrace.EndSpan(ctx, err) }()

	if !r.running.TryLock() {
		r.logger.WithContext(ctx).Infof("capability index reconcile already running, skipping this tick")
		return nil
	}
	defer r.running.Unlock()

	if err = r.indexSync.EnsureInitialized(ctx); err != nil {
		return err
	}
	if err = r.reconcileTools(ctx); err != nil {
		r.logger.WithContext(ctx).Errorf("reconcile function capabilities failed: %v", err)
	}
	return err
}

// reconcileTools makes the function capabilities match the tool table.
func (r *reconciler) reconcileTools(ctx context.Context) error {
	boxIDs, err := r.toolRepo.SelectToolBoxIDsByFilter(ctx, nil)
	if err != nil {
		return err
	}

	desired := make(map[interfaces.CapabilityRef]*interfaces.CapabilityDocument)
	for _, boxID := range boxIDs {
		boxID = strings.TrimSpace(boxID)
		if boxID == "" {
			continue
		}
		tools, err := r.toolRepo.SelectToolByBoxID(ctx, boxID)
		if err != nil {
			// One unreadable box must not delete every other box's capabilities: an incomplete
			// desired set read as complete is a purge.
			return err
		}
		for _, tool := range tools {
			if tool == nil || tool.IsDeleted {
				continue
			}
			desired[toolRef(tool.BoxID, tool.ToolID)] = toolDocument(tool)
		}
	}
	return r.apply(ctx, interfaces.CapabilityTypeFunction, desired, nil)
}

// SyncTools makes the index match the named tools of one box.
//
// It re-reads the rows instead of taking the caller's in-memory copy, and that is the whole point:
// a tool write happens inside a transaction, and an index written from the value the caller was
// about to commit would keep a row the transaction then rolled back. Reading the table means what
// lands in the index is whatever the table now says — a tool that is absent or deleted is removed
// — so this is safe to call after a write whether or not the write survived.
func (r *reconciler) SyncTools(ctx context.Context, boxID string, toolIDs []string) error {
	boxID = strings.TrimSpace(boxID)
	if boxID == "" || len(toolIDs) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(toolIDs))
	ids := make([]string, 0, len(toolIDs))
	for _, id := range toolIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, dup := wanted[id]; dup {
			continue
		}
		wanted[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}

	tools, err := r.toolRepo.SelectToolBoxByID(ctx, boxID, ids)
	if err != nil {
		return err
	}

	var errs []error
	live := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.IsDeleted {
			continue
		}
		live[tool.ToolID] = struct{}{}
		if err := r.indexSync.UpsertCapability(ctx, toolDocument(tool)); err != nil {
			errs = append(errs, err)
		}
	}
	for id := range wanted {
		if _, ok := live[id]; ok {
			continue
		}
		ref := interfaces.CapabilityRef{
			CapabilityType: interfaces.CapabilityTypeFunction,
			OwnerID:        boxID,
			CapabilityID:   id,
		}
		if err := r.indexSync.DeleteCapability(ctx, ref); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// SyncBox makes the index match every tool of one box, adding what is new and removing what is
// gone. It is the shape a box-wide write takes: an import, or a box whose tools were replaced.
func (r *reconciler) SyncBox(ctx context.Context, boxID string) error {
	boxID = strings.TrimSpace(boxID)
	if boxID == "" {
		return nil
	}
	tools, err := r.toolRepo.SelectToolByBoxID(ctx, boxID)
	if err != nil {
		return err
	}
	desired := make(map[interfaces.CapabilityRef]*interfaces.CapabilityDocument, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.IsDeleted {
			continue
		}
		desired[toolRef(tool.BoxID, tool.ToolID)] = toolDocument(tool)
	}

	indexed, err := r.indexSync.ListIndexedByOwner(ctx, interfaces.CapabilityTypeFunction, boxID)
	if err != nil {
		return err
	}
	var errs []error
	for _, doc := range desired {
		if err := r.indexSync.UpsertCapability(ctx, doc); err != nil {
			errs = append(errs, err)
		}
	}
	for _, entry := range indexed {
		if _, ok := desired[entry.CapabilityRef]; ok {
			continue
		}
		if err := r.indexSync.DeleteCapability(ctx, entry.CapabilityRef); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ForgetBox removes every tool of a deleted box.
func (r *reconciler) ForgetBox(ctx context.Context, boxID string) error {
	boxID = strings.TrimSpace(boxID)
	if boxID == "" {
		return nil
	}
	return r.indexSync.DeleteOwner(ctx, interfaces.CapabilityTypeFunction, boxID)
}

// SyncToolsAsync runs SyncTools off the request path.
//
// The index write costs an embedding round trip per changed tool, which does not belong in the
// latency of creating one. Cancellation is dropped but the context values are kept: the request's
// context is done the moment its response is written, and the downstream client reads its headers
// from there.
func (r *reconciler) SyncToolsAsync(ctx context.Context, boxID string, toolIDs []string) {
	detached := context.WithoutCancel(ctx)
	go func() {
		if err := r.SyncTools(detached, boxID, toolIDs); err != nil {
			r.logger.WithContext(detached).Warnf("sync tools into capability index failed, box_id=%s, tools=%d, err=%v",
				boxID, len(toolIDs), err)
		}
	}()
}

// SyncBoxAsync runs SyncBox off the request path.
func (r *reconciler) SyncBoxAsync(ctx context.Context, boxID string) {
	detached := context.WithoutCancel(ctx)
	go func() {
		if err := r.SyncBox(detached, boxID); err != nil {
			r.logger.WithContext(detached).Warnf("sync tool box into capability index failed, box_id=%s, err=%v", boxID, err)
		}
	}()
}

// ForgetBoxAsync runs ForgetBox off the request path.
func (r *reconciler) ForgetBoxAsync(ctx context.Context, boxID string) {
	detached := context.WithoutCancel(ctx)
	go func() {
		if err := r.ForgetBox(detached, boxID); err != nil {
			r.logger.WithContext(detached).Warnf("purge tool box from capability index failed, box_id=%s, err=%v", boxID, err)
		}
	}()
}

func toolRef(boxID, toolID string) interfaces.CapabilityRef {
	return interfaces.CapabilityRef{
		CapabilityType: interfaces.CapabilityTypeFunction,
		OwnerID:        boxID,
		CapabilityID:   toolID,
	}
}

func toolDocument(tool *model.ToolDB) *interfaces.CapabilityDocument {
	return &interfaces.CapabilityDocument{
		CapabilityRef: toolRef(tool.BoxID, tool.ToolID),
		Name:          tool.Name,
		Description:   tool.Description,
		CreateUser:    tool.CreateUser,
		CreateTime:    tool.CreateTime,
		UpdateUser:    tool.UpdateUser,
		UpdateTime:    tool.UpdateTime,
	}
}

// apply writes what changed and deletes what is gone.
//
// protected, when given, answers whether a document that is absent from the desired set should be
// kept anyway because its owner could not be read this pass.
func (r *reconciler) apply(ctx context.Context, capabilityType string,
	desired map[interfaces.CapabilityRef]*interfaces.CapabilityDocument,
	protected func(interfaces.CapabilityRef) bool) error {
	indexed, err := r.indexSync.ListIndexed(ctx, capabilityType)
	if err != nil {
		return err
	}

	current := make(map[interfaces.CapabilityRef]interfaces.IndexedCapability, len(indexed))
	for _, entry := range indexed {
		current[entry.CapabilityRef] = entry
	}

	var errs []error
	written, deleted := 0, 0
	for ref, doc := range desired {
		existing, ok := current[ref]
		// Name and description are the whole embedding input, so an unchanged pair means an
		// unchanged vector. Skipping those is what keeps a pass from re-embedding the platform.
		if ok && existing.Name == doc.Name && existing.Description == doc.Description {
			continue
		}
		if err := r.indexSync.UpsertCapability(ctx, doc); err != nil {
			errs = append(errs, err)
			continue
		}
		written++
	}
	for ref := range current {
		if _, ok := desired[ref]; ok {
			continue
		}
		if protected != nil && protected(ref) {
			continue
		}
		if err := r.indexSync.DeleteCapability(ctx, ref); err != nil {
			errs = append(errs, err)
			continue
		}
		deleted++
	}
	r.logger.WithContext(ctx).Infof("capability index reconciled, type=%s, desired=%d, indexed=%d, written=%d, deleted=%d, errors=%d",
		capabilityType, len(desired), len(current), written, deleted, len(errs))
	return errors.Join(errs...)
}
