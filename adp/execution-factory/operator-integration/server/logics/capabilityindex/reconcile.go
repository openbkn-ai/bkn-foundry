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
	// boxRepo answers what kind of tool box a tool belongs to. The product splits Function tools
	// into "API tools" and "functions" by that kind, and the tool row does not carry it.
	boxRepo model.IToolboxDB
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
			boxRepo:   dbaccess.NewToolboxDB(),
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

	boxes, err := r.boxInfos(ctx, boxIDs)
	if err != nil {
		// Same rule as an unreadable tool list below: an incomplete desired set applied as if it
		// were complete is a purge, and a box read that failed would make every tool look
		// unpublished. Stop here; the index keeps what it has until a pass can read everything.
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
			if !admissible(tool, boxes[tool.BoxID]) {
				continue
			}
			desired[toolRef(tool.BoxID, tool.ToolID)] = toolDocument(tool, boxes[tool.BoxID].kind)
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

	boxes, err := r.boxInfos(ctx, []string{boxID})
	if err != nil {
		// The box's state decides whether these tools are written or removed; without it neither
		// is safe. Leave the index as it is — the next event or the full pass will read it again.
		return err
	}
	box := boxes[boxID]
	var errs []error
	live := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		// A tool that fails admission is removed exactly like a deleted one: a disabled tool, or
		// one whose box is no longer published, must leave the index on the event that made it
		// so, or the next full pass would keep re-asserting a document nobody can call.
		if !admissible(tool, box) {
			continue
		}
		live[tool.ToolID] = struct{}{}
		if err := r.indexSync.UpsertCapability(ctx, toolDocument(tool, box.kind)); err != nil {
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
	boxes, err := r.boxInfos(ctx, []string{boxID})
	if err != nil {
		return err
	}
	box := boxes[boxID]
	desired := make(map[interfaces.CapabilityRef]*interfaces.CapabilityDocument, len(tools))
	for _, tool := range tools {
		if !admissible(tool, box) {
			continue
		}
		desired[toolRef(tool.BoxID, tool.ToolID)] = toolDocument(tool, box.kind)
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

// boxInfo is what admission and the document need to know about a tool's box: which product
// kind it is, and whether it is published.
type boxInfo struct {
	kind      string
	published bool
}

// boxInfos reads the boxes behind a batch of tools in one query.
//
// A read that fails is an error, not an empty answer. The distinction matters because of what the
// callers do with a box they cannot find: they treat it as unpublished and remove its tools. That
// is right for a box that is genuinely gone, and catastrophic for a query that merely failed —
// applied to a full pass, one database hiccup would read as "every box is unpublished" and purge
// the whole function index until the next successful pass re-embedded everything. So a failed
// read stops the caller from touching the index at all; the next event or pass reads again.
func (r *reconciler) boxInfos(ctx context.Context, boxIDs []string) (map[string]boxInfo, error) {
	infos := make(map[string]boxInfo, len(boxIDs))
	if r.boxRepo == nil || len(boxIDs) == 0 {
		return infos, nil
	}
	boxes, err := r.boxRepo.SelectListByBoxIDs(ctx, boxIDs)
	if err != nil {
		return nil, err
	}
	for _, box := range boxes {
		if box != nil {
			infos[box.BoxID] = boxInfo{
				kind:      box.MetadataType,
				published: box.Status == string(interfaces.BizStatusPublished),
			}
		}
	}
	return infos, nil
}

// admissible is the one rule for whether a tool belongs in the capability index (#1443).
//
// The index is a catalogue of what an agent can call, not a copy of the tool table. A tool is
// callable when its box is published and it is itself enabled; a draft box, an unpublished one,
// or a disabled tool is management state that must not surface in retrieval. Every writer — the
// incremental syncs and the full pass — asks this same function, so an event and the next
// reconcile can never disagree about a row.
func admissible(tool *model.ToolDB, box boxInfo) bool {
	if tool == nil || tool.IsDeleted {
		return false
	}
	if !box.published {
		return false
	}
	return tool.Status == string(interfaces.ToolStatusTypeEnabled)
}

func toolRef(boxID, toolID string) interfaces.CapabilityRef {
	return interfaces.CapabilityRef{
		CapabilityType: interfaces.CapabilityTypeFunction,
		OwnerID:        boxID,
		CapabilityID:   toolID,
	}
}

func toolDocument(tool *model.ToolDB, metadataType string) *interfaces.CapabilityDocument {
	return &interfaces.CapabilityDocument{
		CapabilityRef: toolRef(tool.BoxID, tool.ToolID),
		MetadataType:  metadataType,
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
		// The kind is compared too: it is not part of the vector, but a box converted between
		// openapi and function would otherwise keep its old label forever.
		if ok && existing.Name == doc.Name && existing.Description == doc.Description &&
			existing.MetadataType == doc.MetadataType {
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
