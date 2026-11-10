// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package kn_version publishes and switches the versions of knowledge networks.
package kn_version

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"github.com/openbkn-ai/licverify"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
)

var (
	serviceOnce sync.Once
	serviceInst interfaces.KNVersionService
)

type service struct {
	db  *sql.DB
	va  interfaces.KNVersionAccess
	bc  interfaces.BranchCopier
	ps  interfaces.PermissionService
	pub interfaces.KNMainPublisher

	// fullHistory reports the Professional edition, which keeps every version.
	fullHistory func() bool
	now         func() int64
}

func NewKNVersionService(appSetting *common.AppSetting, pub interfaces.KNMainPublisher) interfaces.KNVersionService {
	serviceOnce.Do(func() {
		serviceInst = &service{
			db:  logics.DB,
			va:  logics.KNVA,
			bc:  logics.BC,
			ps:  permission.NewPermissionService(appSetting),
			pub: pub,
			fullHistory: func() bool {
				return entitlement.AtLeast(licverify.EditionProfessional)
			},
			now: func() int64 { return time.Now().UnixMilli() },
		}
	})
	return serviceInst
}

func newHTTPError(ctx context.Context, status int, code string) *httperrors.HTTPError {
	return httperrors.NewHTTPError(ctx, status, code)
}

func internalError(ctx context.Context, err error) error {
	var httpErr *httperrors.HTTPError
	if errors.As(err, &httpErr) {
		return httpErr
	}
	otellog.LogError(ctx, "Knowledge network version operation failed", err)
	return newHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_KNVersion_InternalError).
		WithErrorDetails(err.Error())
}

func (s *service) operator(ctx context.Context) interfaces.AccountInfo {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	return interfaces.AccountInfo{ID: account.ID, Type: account.Type}
}

func (s *service) checkPermission(ctx context.Context, knID, operation string) error {
	return s.ps.CheckPermission(ctx, interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: knID},
		[]string{operation})
}

func (s *service) view(state *interfaces.KNVersionState) *interfaces.KNVersioningView {
	if state == nil {
		return &interfaces.KNVersioningView{Enabled: false, FullHistory: s.fullHistory()}
	}
	return &interfaces.KNVersioningView{
		Enabled:         true,
		Mode:            state.Mode,
		RunningVersion:  state.RunningVersion,
		PreviousVersion: state.PreviousVersion,
		FullHistory:     s.fullHistory(),
		Draft: &interfaces.KNDraftView{
			BaseVersion: state.DraftBaseVersion,
			HasChanges:  state.DraftHasChanges(),
			Outdated:    state.DraftOutdated(),
			Revision:    state.DraftRevision,
		},
	}
}

// operation identifies a version operation for idempotency: its event types and the version it
// targets, 0 when the request names none.
type operation struct {
	types  []string
	target int
	key    string
	replay func(ctx context.Context, event *interfaces.KNVersionEvent) (any, error)
}

// errReplayed rolls back a transaction that found the idempotency key already used.
type errReplayed struct{ event *interfaces.KNVersionEvent }

func (e *errReplayed) Error() string { return "idempotency key already used" }

func eventTarget(event *interfaces.KNVersionEvent) int {
	switch event.Type {
	case interfaces.KNVersionEventCopy:
		return event.FromVersion
	case interfaces.KNVersionEventSwitch, interfaces.KNVersionEventDeprecate,
		interfaces.KNVersionEventRestore, interfaces.KNVersionEventPurge:
		return event.ToVersion
	}
	return 0
}

func (s *service) replay(ctx context.Context, op *operation, event *interfaces.KNVersionEvent) (any, error) {
	if !slices.Contains(op.types, event.Type) || (op.target != 0 && op.target != eventTarget(event)) {
		return nil, newHTTPError(ctx, http.StatusUnprocessableEntity, berrors.BknBackend_KNVersion_IdempotencyKeyReused).
			WithErrorDetails(map[string]any{"operation": event.Type, "version": eventTarget(event)})
	}
	return op.replay(ctx, event)
}

// findReplay returns the result of an earlier request with the same idempotency key, or nil.
func (s *service) findReplay(ctx context.Context, tx *sql.Tx, knID string, op *operation) (*interfaces.KNVersionEvent, error) {
	if op.key == "" {
		return nil, nil
	}
	return s.va.GetEventByIdempotencyKey(ctx, tx, knID, op.key)
}

func (s *service) replayState(ctx context.Context, event *interfaces.KNVersionEvent) (any, error) {
	return s.GetState(ctx, event.KNID)
}

func (s *service) replayVersion(ctx context.Context, event *interfaces.KNVersionEvent) (any, error) {
	return s.GetVersion(ctx, event.KNID, event.ToVersion)
}

// lockState locks the network exclusively and its state row, in that order, and checks the
// network is versioned and not being disabled.
func (s *service) lockState(ctx context.Context, tx *sql.Tx, knID string) (*interfaces.KNVersionState, error) {
	state, err := s.lockAnyState(ctx, tx, knID)
	if err != nil {
		return nil, err
	}
	if state.Mode == interfaces.KNVersioningDisabling {
		return nil, newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersioningDisabling)
	}
	return state, nil
}

// lockAnyState is lockState for the disable flow, which also runs on a disabling network.
func (s *service) lockAnyState(ctx context.Context, tx *sql.Tx, knID string) (*interfaces.KNVersionState, error) {
	exists, err := s.va.LockNetwork(ctx, tx, knID, true)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, newHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)
	}
	state, err := s.va.GetState(ctx, tx, knID, true)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, newHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNVersion_NotVersioned)
	}
	return state, nil
}

func (s *service) newEvent(ctx context.Context, knID, eventType string, from, to int, notes, key string) *interfaces.KNVersionEvent {
	return &interfaces.KNVersionEvent{
		ID: strings.ReplaceAll(uuid.NewString(), "-", ""), KNID: knID, Type: eventType,
		FromVersion: from, ToVersion: to, Notes: notes, Operator: s.operator(ctx),
		CreateTime: s.now(), IdempotencyKey: key,
	}
}

// run executes a version operation: replay by idempotency key before and inside the
// transaction, then fn. inTx runs fn either in a plain transaction or inside the publisher of
// main when the operation replaces main.
func (s *service) run(ctx context.Context, knID string, op *operation,
	inTx func(context.Context, func(context.Context, *sql.Tx) error) error,
	fn func(context.Context, *sql.Tx) (any, error)) (any, error) {
	event, err := s.findReplay(ctx, nil, knID, op)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if event != nil {
		return s.replay(ctx, op, event)
	}
	var result any
	err = inTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if event, err := s.findReplayLocked(ctx, tx, knID, op); err != nil || event != nil {
			if event != nil {
				return &errReplayed{event: event}
			}
			return err
		}
		var err error
		result, err = fn(ctx, tx)
		return err
	})
	var replayed *errReplayed
	if errors.As(err, &replayed) {
		return s.replay(ctx, op, replayed.event)
	}
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return result, nil
}

// findReplayLocked repeats the idempotency lookup after the network row is locked, so that of
// concurrent requests with one key only the first writes its event.
func (s *service) findReplayLocked(ctx context.Context, tx *sql.Tx, knID string, op *operation) (*interfaces.KNVersionEvent, error) {
	if op.key == "" {
		return nil, nil
	}
	if _, err := s.va.LockNetwork(ctx, tx, knID, true); err != nil {
		return nil, err
	}
	return s.findReplay(ctx, tx, knID, op)
}

func (s *service) plainTx(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(ctx, tx); err != nil {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && rollbackErr != sql.ErrTxDone {
			otellog.LogError(ctx, "Rollback version operation failed", rollbackErr)
		}
		return err
	}
	return tx.Commit()
}

func (s *service) draftTx(knID string) func(context.Context, func(context.Context, *sql.Tx) error) error {
	return func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
		return s.pub.ReplaceKNDraft(ctx, knID, fn)
	}
}

func (s *service) mainTx(knID, src string) func(context.Context, func(context.Context, *sql.Tx) error) error {
	return func(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
		return s.pub.PublishKNMain(ctx, knID, src, fn)
	}
}

func (s *service) Enable(ctx context.Context, knID string, req *interfaces.KNEnableVersioningRequest) (*interfaces.KNVersioningView, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Enable knowledge network versioning")
	defer span.End()
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return nil, err
	}
	op := &operation{types: []string{interfaces.KNVersionEventEnable}, key: req.IdempotencyKey, replay: s.replayState}
	result, err := s.run(ctx, knID, op, s.draftTx(knID), func(ctx context.Context, tx *sql.Tx) (any, error) {
		exists, err := s.va.LockNetwork(ctx, tx, knID, true)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, newHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KnowledgeNetwork_NotFound)
		}
		state, err := s.va.GetState(ctx, tx, knID, true)
		if err != nil {
			return nil, err
		}
		if state != nil {
			return nil, newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_AlreadyVersioned)
		}
		if err := s.checkReservedBranches(ctx, tx, knID); err != nil {
			return nil, err
		}
		latest, err := s.va.MaxVersion(ctx, tx, knID)
		if err != nil {
			return nil, err
		}
		n := latest + 1
		if err := s.bc.ReplaceBranch(ctx, tx, knID, interfaces.MAIN_BRANCH, interfaces.VersionBranch(n)); err != nil {
			return nil, err
		}
		if err := s.bc.ReplaceBranch(ctx, tx, knID, interfaces.MAIN_BRANCH, interfaces.DRAFT_BRANCH); err != nil {
			return nil, err
		}
		now, who := s.now(), s.operator(ctx)
		state = &interfaces.KNVersionState{
			KNID: knID, Mode: interfaces.KNVersioningEnabled, RunningVersion: n, NextVersion: n + 1,
			DraftBaseVersion: n, DraftBasedOnRunning: n, DraftIndexRevision: -1,
			EnabledBy: who.ID, EnabledByType: who.Type, EnableTime: now, UpdateTime: now,
		}
		if err := s.va.CreateState(ctx, tx, state); err != nil {
			return nil, err
		}
		if err := s.va.CreateVersion(ctx, tx, &interfaces.KNVersion{
			KNID: knID, Version: n, Label: req.Label, Status: interfaces.KNVersionRunning,
			Origin: interfaces.KNVersionOriginEnable, Notes: req.Notes,
			Publisher: who, PublishTime: now, StatusBy: who, StatusTime: now,
		}); err != nil {
			return nil, err
		}
		if err := s.va.CreateEvent(ctx, tx, s.newEvent(ctx, knID, interfaces.KNVersionEventEnable,
			0, n, req.Notes, req.IdempotencyKey)); err != nil {
			return nil, err
		}
		return s.view(state), nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*interfaces.KNVersioningView), nil
}

// checkReservedBranches refuses to enable a network that already has data on draft or v<N>.
func (s *service) checkReservedBranches(ctx context.Context, tx *sql.Tx, knID string) error {
	branches, err := s.bc.BranchesWithData(ctx, tx, knID)
	if err != nil {
		return err
	}
	reserved := []string{}
	for _, branch := range branches {
		if IsReservedBranch(branch) {
			reserved = append(reserved, branch)
		}
	}
	if len(reserved) > 0 {
		return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_ReservedBranchInUse).
			WithErrorDetails(map[string]any{"branches": reserved})
	}
	return nil
}

// IsReservedBranch reports draft and v<digits>, the branches a versioned network keeps for itself.
func IsReservedBranch(branch string) bool {
	if branch == interfaces.DRAFT_BRANCH {
		return true
	}
	digits, ok := strings.CutPrefix(branch, interfaces.VERSION_BRANCH_PREFIX)
	if !ok || digits == "" {
		return false
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *service) GetState(ctx context.Context, knID string) (*interfaces.KNVersioningView, error) {
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		return nil, err
	}
	state, err := s.va.GetState(ctx, nil, knID, false)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return s.view(state), nil
}

func (s *service) ListVersions(ctx context.Context, knID string, query *interfaces.KNVersionsQuery) ([]*interfaces.KNVersion, int, error) {
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		return nil, 0, err
	}
	versions, total, err := s.va.ListVersions(ctx, knID, query)
	if err != nil {
		return nil, 0, internalError(ctx, err)
	}
	return versions, total, nil
}

func (s *service) GetVersion(ctx context.Context, knID string, version int) (*interfaces.KNVersion, error) {
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		return nil, err
	}
	v, err := s.va.GetVersion(ctx, nil, knID, version, false)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if v == nil {
		return nil, newHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KNVersion_VersionNotFound)
	}
	return v, nil
}

func (s *service) ListEvents(ctx context.Context, knID string, offset, limit int) ([]*interfaces.KNVersionEvent, int, error) {
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		return nil, 0, err
	}
	events, total, err := s.va.ListEvents(ctx, knID, offset, limit)
	if err != nil {
		return nil, 0, internalError(ctx, err)
	}
	return events, total, nil
}

func runningMismatch(ctx context.Context, state *interfaces.KNVersionState) error {
	return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_RunningVersionMismatch).
		WithErrorDetails(map[string]any{"running_version": state.RunningVersion})
}

func revisionMismatch(ctx context.Context, state *interfaces.KNVersionState) error {
	return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_DraftRevisionMismatch).
		WithErrorDetails(map[string]any{"draft_revision": state.DraftRevision})
}

func requireField(ctx context.Context, set bool, name string) error {
	if set {
		return nil
	}
	return newHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNVersion_InvalidParameter).
		WithErrorDetails(name + " is required")
}

func (s *service) Publish(ctx context.Context, knID string, req *interfaces.KNPublishRequest) (*interfaces.KNPublishResult, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Publish knowledge network version")
	defer span.End()
	if err := requireField(ctx, req.ExpectedRunningVersion != nil, "expected_running_version"); err != nil {
		return nil, err
	}
	if err := requireField(ctx, req.ExpectedDraftRevision != nil, "expected_draft_revision"); err != nil {
		return nil, err
	}
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return nil, err
	}
	// Refuse early outside the transaction; the transaction checks again under the lock.
	state, err := s.va.GetState(ctx, nil, knID, false)
	if err != nil {
		return nil, internalError(ctx, err)
	}
	if state == nil {
		return nil, newHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNVersion_NotVersioned)
	}
	op := &operation{types: []string{interfaces.KNVersionEventPublish}, key: req.IdempotencyKey,
		replay: func(ctx context.Context, event *interfaces.KNVersionEvent) (any, error) {
			v, err := s.GetVersion(ctx, event.KNID, event.ToVersion)
			if err != nil {
				return nil, err
			}
			return &interfaces.KNPublishResult{KNVersion: v}, nil
		}}
	if req.IdempotencyKey == "" {
		if err := s.checkPublishable(ctx, state, req); err != nil {
			return nil, err
		}
	}
	result, err := s.run(ctx, knID, op, s.mainTx(knID, interfaces.DRAFT_BRANCH), func(ctx context.Context, tx *sql.Tx) (any, error) {
		state, err := s.lockState(ctx, tx, knID)
		if err != nil {
			return nil, err
		}
		if err := s.checkPublishable(ctx, state, req); err != nil {
			return nil, err
		}
		n, previous, now, who := state.NextVersion, state.RunningVersion, s.now(), s.operator(ctx)
		if err := s.bc.ReplaceBranch(ctx, tx, knID, interfaces.DRAFT_BRANCH, interfaces.VersionBranch(n)); err != nil {
			return nil, err
		}
		if err := s.bc.ReplaceBranch(ctx, tx, knID, interfaces.VersionBranch(n), interfaces.MAIN_BRANCH); err != nil {
			return nil, err
		}
		version := &interfaces.KNVersion{
			KNID: knID, Version: n, Label: req.Label, Status: interfaces.KNVersionRunning,
			Origin: interfaces.KNVersionOriginPublish, BaseVersion: state.DraftBaseVersion,
			DraftRevision: state.DraftRevision, Notes: req.Notes, Source: req.Source,
			Publisher: who, PublishTime: now, StatusBy: who, StatusTime: now,
		}
		if err := s.va.CreateVersion(ctx, tx, version); err != nil {
			return nil, err
		}
		if previous > 0 {
			if err := s.setStatus(ctx, tx, knID, previous, interfaces.KNVersionHistory); err != nil {
				return nil, err
			}
		}
		purged := 0
		if !s.fullHistory() && state.PreviousVersion > 0 {
			purged = state.PreviousVersion
			if err := s.purgeInTx(ctx, tx, knID, purged); err != nil {
				return nil, err
			}
			event := s.newEvent(ctx, knID, interfaces.KNVersionEventPurge, n, purged, "", "")
			event.Reason = interfaces.KNVersionReasonRetention
			if err := s.va.CreateEvent(ctx, tx, event); err != nil {
				return nil, err
			}
		}
		state.RunningVersion, state.PreviousVersion, state.NextVersion = n, previous, n+1
		state.DraftBaseVersion, state.DraftBasedOnRunning, state.DraftModified = n, n, false
		state.UpdateTime = now
		if err := s.va.UpdateState(ctx, tx, state); err != nil {
			return nil, err
		}
		if err := s.va.CreateEvent(ctx, tx, s.newEvent(ctx, knID, interfaces.KNVersionEventPublish,
			previous, n, req.Notes, req.IdempotencyKey)); err != nil {
			return nil, err
		}
		return &interfaces.KNPublishResult{KNVersion: version, PurgedVersion: purged}, nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*interfaces.KNPublishResult), nil
}

func (s *service) checkPublishable(ctx context.Context, state *interfaces.KNVersionState, req *interfaces.KNPublishRequest) error {
	if state.Mode == interfaces.KNVersioningDisabling {
		return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersioningDisabling)
	}
	if *req.ExpectedRunningVersion != state.RunningVersion {
		return runningMismatch(ctx, state)
	}
	if *req.ExpectedDraftRevision != state.DraftRevision {
		return revisionMismatch(ctx, state)
	}
	// An unpublished network publishes its first version even without edits.
	if state.RunningVersion > 0 && !state.DraftHasChanges() {
		return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_NothingToPublish)
	}
	if state.DraftOutdated() && !req.AllowOutdatedBase {
		return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_DraftBaseOutdated).
			WithErrorDetails(map[string]any{
				"running_version":          state.RunningVersion,
				"draft_base_version":       state.DraftBaseVersion,
				"based_on_running_version": state.DraftBasedOnRunning,
			})
	}
	return nil
}

func (s *service) setStatus(ctx context.Context, tx *sql.Tx, knID string, version int, status string) error {
	return s.va.UpdateVersionStatus(ctx, tx, &interfaces.KNVersion{
		KNID: knID, Version: version, Status: status, StatusBy: s.operator(ctx), StatusTime: s.now(),
	})
}

// purgeInTx deletes the content of a version and keeps its record.
func (s *service) purgeInTx(ctx context.Context, tx *sql.Tx, knID string, version int) error {
	if err := s.bc.DeleteBranch(ctx, tx, knID, interfaces.VersionBranch(version)); err != nil {
		return err
	}
	return s.setStatus(ctx, tx, knID, version, interfaces.KNVersionPurged)
}

// editionAllows reports whether the Community edition may use version k: only the running and
// the previous version are kept there.
func (s *service) editionAllows(state *interfaces.KNVersionState, k int) bool {
	return s.fullHistory() || k == state.RunningVersion || k == state.PreviousVersion
}

func editionRestricted(ctx context.Context) error {
	return newHTTPError(ctx, http.StatusForbidden, berrors.BknBackend_KNVersion_EditionRestricted).
		WithErrorDetails(map[string]any{"edition": string(licverify.EditionCommunity)})
}

// lockVersion locks version k and checks it exists.
func (s *service) lockVersion(ctx context.Context, tx *sql.Tx, knID string, k int) (*interfaces.KNVersion, error) {
	v, err := s.va.GetVersion(ctx, tx, knID, k, true)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, newHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KNVersion_VersionNotFound)
	}
	return v, nil
}

func (s *service) Activate(ctx context.Context, knID string, k int, req *interfaces.KNActivateRequest) (*interfaces.KNVersioningView, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Activate knowledge network version")
	defer span.End()
	if err := requireField(ctx, req.ExpectedRunningVersion != nil, "expected_running_version"); err != nil {
		return nil, err
	}
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return nil, err
	}
	op := &operation{types: []string{interfaces.KNVersionEventSwitch}, target: k, key: req.IdempotencyKey, replay: s.replayState}
	// Check the target before the main publisher reads its content.
	if req.IdempotencyKey == "" {
		if err := s.checkActivatable(ctx, nil, knID, k, req); err != nil {
			return nil, err
		}
	}
	result, err := s.run(ctx, knID, op, s.mainTx(knID, interfaces.VersionBranch(k)), func(ctx context.Context, tx *sql.Tx) (any, error) {
		state, err := s.lockState(ctx, tx, knID)
		if err != nil {
			return nil, err
		}
		if *req.ExpectedRunningVersion != state.RunningVersion {
			return nil, runningMismatch(ctx, state)
		}
		if !s.editionAllows(state, k) {
			return nil, editionRestricted(ctx)
		}
		v, err := s.lockVersion(ctx, tx, knID, k)
		if err != nil {
			return nil, err
		}
		if v.Status != interfaces.KNVersionHistory {
			return nil, newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersionNotActivatable).
				WithErrorDetails(map[string]any{"status": v.Status})
		}
		if err := s.bc.ReplaceBranch(ctx, tx, knID, interfaces.VersionBranch(k), interfaces.MAIN_BRANCH); err != nil {
			return nil, err
		}
		previous := state.RunningVersion
		if err := s.setStatus(ctx, tx, knID, k, interfaces.KNVersionRunning); err != nil {
			return nil, err
		}
		if err := s.setStatus(ctx, tx, knID, previous, interfaces.KNVersionHistory); err != nil {
			return nil, err
		}
		state.RunningVersion, state.PreviousVersion, state.UpdateTime = k, previous, s.now()
		if err := s.va.UpdateState(ctx, tx, state); err != nil {
			return nil, err
		}
		if err := s.va.CreateEvent(ctx, tx, s.newEvent(ctx, knID, interfaces.KNVersionEventSwitch,
			previous, k, req.Notes, req.IdempotencyKey)); err != nil {
			return nil, err
		}
		return s.view(state), nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*interfaces.KNVersioningView), nil
}

// checkActivatable refuses early, before the content of version k is read for the proxy plan.
func (s *service) checkActivatable(ctx context.Context, tx *sql.Tx, knID string, k int, req *interfaces.KNActivateRequest) error {
	state, err := s.va.GetState(ctx, tx, knID, false)
	if err != nil {
		return internalError(ctx, err)
	}
	if state == nil {
		return newHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KNVersion_NotVersioned)
	}
	if *req.ExpectedRunningVersion != state.RunningVersion {
		return runningMismatch(ctx, state)
	}
	if !s.editionAllows(state, k) {
		return editionRestricted(ctx)
	}
	v, err := s.va.GetVersion(ctx, tx, knID, k, false)
	if err != nil {
		return internalError(ctx, err)
	}
	if v == nil {
		return newHTTPError(ctx, http.StatusNotFound, berrors.BknBackend_KNVersion_VersionNotFound)
	}
	if v.Status != interfaces.KNVersionHistory {
		return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersionNotActivatable).
			WithErrorDetails(map[string]any{"status": v.Status})
	}
	return nil
}

func (s *service) ResetDraft(ctx context.Context, knID string, req *interfaces.KNResetDraftRequest) (*interfaces.KNVersioningView, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Reset knowledge network draft")
	defer span.End()
	if err := requireField(ctx, req.ExpectedDraftRevision != nil, "expected_draft_revision"); err != nil {
		return nil, err
	}
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return nil, err
	}
	op := &operation{types: []string{interfaces.KNVersionEventDiscard}, key: req.IdempotencyKey, replay: s.replayState}
	if req.FromVersion != nil {
		op.types, op.target = []string{interfaces.KNVersionEventCopy}, *req.FromVersion
	}
	result, err := s.run(ctx, knID, op, s.draftTx(knID), func(ctx context.Context, tx *sql.Tx) (any, error) {
		state, err := s.lockState(ctx, tx, knID)
		if err != nil {
			return nil, err
		}
		k, eventType := state.RunningVersion, interfaces.KNVersionEventDiscard
		if req.FromVersion != nil {
			k, eventType = *req.FromVersion, interfaces.KNVersionEventCopy
			if !s.editionAllows(state, k) {
				return nil, editionRestricted(ctx)
			}
		}
		if *req.ExpectedDraftRevision != state.DraftRevision {
			return nil, revisionMismatch(ctx, state)
		}
		if state.DraftHasChanges() && !req.OverwriteChanges {
			return nil, newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_DraftHasChanges).
				WithErrorDetails(map[string]any{"draft": s.view(state).Draft})
		}
		if k == 0 {
			// Discarding the draft of an unpublished network leaves only its network row,
			// whose content follows main, which has no definitions either.
			if err := s.bc.ReplaceBranch(ctx, tx, knID, interfaces.MAIN_BRANCH, interfaces.DRAFT_BRANCH); err != nil {
				return nil, err
			}
		} else {
			v, err := s.lockVersion(ctx, tx, knID, k)
			if err != nil {
				return nil, err
			}
			if v.Status == interfaces.KNVersionPurged {
				return nil, newHTTPError(ctx, http.StatusGone, berrors.BknBackend_KNVersion_VersionPurged)
			}
			if err := s.bc.ReplaceBranch(ctx, tx, knID, interfaces.VersionBranch(k), interfaces.DRAFT_BRANCH); err != nil {
				return nil, err
			}
		}
		state.DraftBaseVersion, state.DraftBasedOnRunning, state.DraftModified = k, state.RunningVersion, false
		state.DraftRevision++
		state.UpdateTime = s.now()
		if err := s.va.UpdateState(ctx, tx, state); err != nil {
			return nil, err
		}
		from := k
		if eventType == interfaces.KNVersionEventDiscard {
			from = state.RunningVersion
		}
		if err := s.va.CreateEvent(ctx, tx, s.newEvent(ctx, knID, eventType, from, 0, "", req.IdempotencyKey)); err != nil {
			return nil, err
		}
		return s.view(state), nil
	})
	if err != nil {
		return nil, err
	}
	return result.(*interfaces.KNVersioningView), nil
}

// changeStatus runs deprecate, restore and purge, which only the Professional edition offers.
func (s *service) changeStatus(ctx context.Context, knID string, k int, req *interfaces.KNVersionStatusRequest,
	eventType, permissionOp string, apply func(context.Context, *sql.Tx, *interfaces.KNVersionState, *interfaces.KNVersion) error) (*interfaces.KNVersion, error) {
	if !s.fullHistory() {
		return nil, editionRestricted(ctx)
	}
	if err := s.checkPermission(ctx, knID, permissionOp); err != nil {
		return nil, err
	}
	op := &operation{types: []string{eventType}, target: k, key: req.IdempotencyKey, replay: s.replayVersion}
	result, err := s.run(ctx, knID, op, s.plainTx, func(ctx context.Context, tx *sql.Tx) (any, error) {
		state, err := s.lockState(ctx, tx, knID)
		if err != nil {
			return nil, err
		}
		v, err := s.lockVersion(ctx, tx, knID, k)
		if err != nil {
			return nil, err
		}
		if err := apply(ctx, tx, state, v); err != nil {
			return nil, err
		}
		if err := s.va.CreateEvent(ctx, tx, s.newEvent(ctx, knID, eventType, state.RunningVersion, k,
			req.Notes, req.IdempotencyKey)); err != nil {
			return nil, err
		}
		return s.va.GetVersion(ctx, tx, knID, k, false)
	})
	if err != nil {
		return nil, err
	}
	return result.(*interfaces.KNVersion), nil
}

func (s *service) Deprecate(ctx context.Context, knID string, k int, req *interfaces.KNVersionStatusRequest) (*interfaces.KNVersion, error) {
	return s.changeStatus(ctx, knID, k, req, interfaces.KNVersionEventDeprecate, interfaces.OPERATION_TYPE_MODIFY,
		func(ctx context.Context, tx *sql.Tx, _ *interfaces.KNVersionState, v *interfaces.KNVersion) error {
			if v.Status != interfaces.KNVersionHistory {
				return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersionNotDeprecatable).
					WithErrorDetails(map[string]any{"status": v.Status})
			}
			return s.setStatus(ctx, tx, knID, k, interfaces.KNVersionDeprecated)
		})
}

func (s *service) Restore(ctx context.Context, knID string, k int, req *interfaces.KNVersionStatusRequest) (*interfaces.KNVersion, error) {
	return s.changeStatus(ctx, knID, k, req, interfaces.KNVersionEventRestore, interfaces.OPERATION_TYPE_MODIFY,
		func(ctx context.Context, tx *sql.Tx, _ *interfaces.KNVersionState, v *interfaces.KNVersion) error {
			if v.Status != interfaces.KNVersionDeprecated {
				return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersionNotRestorable).
					WithErrorDetails(map[string]any{"status": v.Status})
			}
			return s.setStatus(ctx, tx, knID, k, interfaces.KNVersionHistory)
		})
}

func (s *service) Purge(ctx context.Context, knID string, k int, req *interfaces.KNVersionStatusRequest) (*interfaces.KNVersion, error) {
	return s.changeStatus(ctx, knID, k, req, interfaces.KNVersionEventPurge, interfaces.OPERATION_TYPE_DELETE,
		func(ctx context.Context, tx *sql.Tx, state *interfaces.KNVersionState, v *interfaces.KNVersion) error {
			if v.Status == interfaces.KNVersionPurged {
				return newHTTPError(ctx, http.StatusGone, berrors.BknBackend_KNVersion_VersionPurged)
			}
			if k == state.RunningVersion || k == state.DraftBaseVersion {
				return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersionProtected)
			}
			if err := s.purgeInTx(ctx, tx, knID, k); err != nil {
				return err
			}
			if state.PreviousVersion == k {
				state.PreviousVersion, state.UpdateTime = 0, s.now()
				return s.va.UpdateState(ctx, tx, state)
			}
			return nil
		})
}

// Disable takes a network out of versioning (design §12.9). Each step can be repeated: a
// failure leaves the network disabling, with writes paused, and running Disable again resumes.
// main already holds the running version, its concept index and its proxy grants, because
// publish and activate keep them current, so only the versioned rows are left to remove.
func (s *service) Disable(ctx context.Context, knID string, req *interfaces.KNDisableVersioningRequest) (*interfaces.KNVersioningView, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "Disable knowledge network versioning")
	defer span.End()
	if err := s.checkPermission(ctx, knID, interfaces.OPERATION_TYPE_AUTHORIZE); err != nil {
		return nil, err
	}
	// 1. Pause writes, unless the draft has changes the request does not discard.
	err := s.plainTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		state, err := s.lockAnyState(ctx, tx, knID)
		if err != nil {
			return err
		}
		if state.Mode == interfaces.KNVersioningDisabling {
			return nil
		}
		if state.DraftHasChanges() && !req.DiscardDraft {
			return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_DraftHasChanges).
				WithErrorDetails("pass discard_draft=true to drop the unpublished changes")
		}
		state.Mode, state.UpdateTime = interfaces.KNVersioningDisabling, s.now()
		return s.va.UpdateState(ctx, tx, state)
	})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	// 2. Delete the draft and every version branch, keep the version records as purged, and
	// remove the state row. The draft's concept index goes with the draft.
	err = s.draftTx(knID)(ctx, func(ctx context.Context, tx *sql.Tx) error {
		state, err := s.lockAnyState(ctx, tx, knID)
		if err != nil {
			return err
		}
		if state.Mode != interfaces.KNVersioningDisabling {
			return newHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KNVersion_VersioningDisabling)
		}
		branches, err := s.bc.BranchesWithData(ctx, tx, knID)
		if err != nil {
			return err
		}
		for _, branch := range branches {
			if branch == interfaces.MAIN_BRANCH {
				continue
			}
			if err := s.bc.DeleteBranch(ctx, tx, knID, branch); err != nil {
				return err
			}
		}
		versions, _, err := s.va.ListVersions(ctx, knID, &interfaces.KNVersionsQuery{
			Statuses: []string{interfaces.KNVersionRunning, interfaces.KNVersionHistory, interfaces.KNVersionDeprecated},
			Limit:    -1,
		})
		if err != nil {
			return err
		}
		for _, version := range versions {
			if err := s.setStatus(ctx, tx, knID, version.Version, interfaces.KNVersionPurged); err != nil {
				return err
			}
		}
		if err := s.va.CreateEvent(ctx, tx, s.newEvent(ctx, knID, interfaces.KNVersionEventDisable,
			state.RunningVersion, 0, req.Notes, "")); err != nil {
			return err
		}
		return s.va.DeleteState(ctx, tx, knID)
	})
	if err != nil {
		return nil, internalError(ctx, err)
	}
	return s.view(nil), nil
}
