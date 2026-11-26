// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/knowledge_network"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/permission"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/logics/user_mgmt"
	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

type permissionChecker interface {
	CheckPermission(ctx context.Context, resource interfaces.PermissionResource, ops []string) error
}

type networkChecker interface {
	CheckKNExistByID(ctx context.Context, knID, branch string) (string, bool, error)
}

type nameResolver interface {
	GetAccountNames(ctx context.Context, accounts []*interfaces.AccountInfo) error
}

type service struct {
	store       *store
	permissions permissionChecker
	networks    networkChecker
	names       nameResolver
	// recognitions lists the record-event action types of a class in the running version, in the
	// transaction of the write; nil reads them from the store.
	recognitions func(ctx context.Context, tx *sql.Tx, knID, classID string) ([]string, error)
	now          func() int64
}

func newService(appSetting *common.AppSetting, store *store) *service {
	return &service{
		store:       store,
		permissions: permission.NewPermissionService(appSetting),
		networks:    knowledge_network.NewKNService(appSetting),
		names:       user_mgmt.NewUserMgmtService(appSetting),
		now:         func() int64 { return time.Now().UnixMilli() },
	}
}

var _ interfaces.KnEventServiceV1 = (*service)(nil)

// fail turns an error into the HTTP error the caller sees.
func fail(ctx context.Context, operation string, err error) error {
	var httpErr *httperrors.HTTPError
	var invalid invalidInput
	var large tooLarge
	var notAllowed errActionNotAllowed
	var noReason errReasonRequired
	switch {
	case errors.As(err, &httpErr):
		return err
	case errors.As(err, &invalid):
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnEvent_InvalidParameter).WithErrorDetails(invalid.detail)
	case errors.As(err, &large):
		return httperrors.NewHTTPError(ctx, http.StatusRequestEntityTooLarge, berrors.BknBackend_KnEvent_TooLarge).WithErrorDetails(large.detail)
	case errors.As(err, &notAllowed):
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_ActionNotAllowed).
			WithErrorDetails(map[string]string{"action": notAllowed.action, "status": notAllowed.status})
	case errors.As(err, &noReason):
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnEvent_InvalidParameter).WithErrorDetails(noReason.Error())
	}
	logger.Errorf("knowledge-network event %s failed: %v", operation, err)
	return httperrors.NewHTTPError(ctx, http.StatusInternalServerError, berrors.BknBackend_KnEvent_InternalError).
		WithErrorDetails("knowledge-network event operation failed")
}

func notFound(ctx context.Context, code string, detail string) error {
	return httperrors.NewHTTPError(ctx, http.StatusNotFound, code).WithErrorDetails(detail)
}

// ensureNetwork answers 404 for a network that does not exist. Events belong
// to the network, which only its main branch represents.
func (s *service) ensureNetwork(ctx context.Context, knID string) error {
	_, exists, err := s.networks.CheckKNExistByID(ctx, knID, interfaces.MAIN_BRANCH)
	if err != nil {
		return fail(ctx, "network lookup", err)
	}
	if !exists {
		return notFound(ctx, berrors.BknBackend_KnowledgeNetwork_NotFound, knID)
	}
	return nil
}

// open checks the network exists, then the caller's permission on it.
func (s *service) open(ctx context.Context, knID, operation string) error {
	if err := s.ensureNetwork(ctx, knID); err != nil {
		return err
	}
	return s.permissions.CheckPermission(ctx, interfaces.PermissionResource{Type: interfaces.RESOURCE_TYPE_KN, ID: knID}, []string{operation})
}

// write runs fn holding the network's head row. The network is checked again
// under the lock: a deletion that held the lock first has removed it.
func (s *service) write(ctx context.Context, knID, operation string, fn func(*writer) error) error {
	err := s.store.write(ctx, knID, func(w *writer) error {
		if err := s.ensureNetwork(ctx, knID); err != nil {
			return err
		}
		return fn(w)
	})
	if err != nil {
		return fail(ctx, operation, err)
	}
	return nil
}

// actor is the caller with a display name, which is stored with what they
// do. A directory outage leaves the name empty rather than failing the write.
func (s *service) actor(ctx context.Context) interfaces.AccountInfo {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if account.ID == "" || s.names == nil {
		return account
	}
	if err := s.names.GetAccountNames(ctx, []*interfaces.AccountInfo{&account}); err != nil {
		logger.Warnf("knowledge-network event: resolve the name of %s %s: %v", account.Type, account.ID, err)
		account.Name = ""
	}
	if account.Name == "-" {
		account.Name = ""
	}
	return account
}

func (s *service) op(ctx context.Context) op {
	lang := i18n.GetLanguageByCtx(ctx)
	return op{
		now:         s.now(),
		actor:       s.actor(ctx),
		executionID: interfaces.ExecutionIDOf(ctx),
		title: func(typeName, objectName string) string {
			title, err := i18n.Render(lang, "BknBackend.KnEvent.RuleEventTitle",
				map[string]any{"type": typeName, "object": objectName})
			if err != nil {
				return typeName + ": " + objectName
			}
			return title
		},
	}
}

func eventsOf(changes []change) []interfaces.KnEvent {
	events := []interfaces.KnEvent{}
	seen := make(map[*interfaces.KnEvent]bool, len(changes))
	for _, entry := range changes {
		if !seen[entry.event] {
			seen[entry.event] = true
			events = append(events, *entry.event)
		}
	}
	return events
}

// Events.

// eventCursor is the last event of a page: the next page starts below it.
type eventCursor struct {
	recordedAt int64
	id         string
}

func encodeCursor(event *interfaces.KnEvent) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(event.RecordedAt, 10) + ":" + event.ID))
}

func decodeCursor(raw string) (*eventCursor, error) {
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, invalidf("cursor is not valid")
	}
	at, id, found := strings.Cut(string(decoded), ":")
	recordedAt, err := strconv.ParseInt(at, 10, 64)
	if !found || err != nil || id == "" {
		return nil, invalidf("cursor is not valid")
	}
	return &eventCursor{recordedAt: recordedAt, id: id}, nil
}

var eventStatuses = map[string]bool{
	interfaces.KnEventUnverified: true, interfaces.KnEventVerified: true, interfaces.KnEventRejected: true,
	interfaces.KnEventDuplicate: true, interfaces.KnEventClosed: true,
}

func (s *service) ListEvents(ctx context.Context, knID string, filter interfaces.KnEventFilter) (interfaces.KnEventList, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		return interfaces.KnEventList{}, err
	}
	for _, status := range filter.Statuses {
		if !eventStatuses[status] {
			return interfaces.KnEventList{}, fail(ctx, "event list", invalidf("unknown status %q", status))
		}
	}
	if err := checkSpan(filter.From, filter.To); err != nil {
		return interfaces.KnEventList{}, fail(ctx, "event list", err)
	}
	after, err := decodeCursor(filter.Cursor)
	if err != nil {
		return interfaces.KnEventList{}, fail(ctx, "event list", err)
	}
	switch {
	case filter.Limit <= 0:
		filter.Limit = interfaces.KnEventListDefaultLimit
	case filter.Limit > interfaces.KnEventListMaxLimit:
		filter.Limit = interfaces.KnEventListMaxLimit
	}
	events, err := s.store.listEvents(ctx, knID, filter, after)
	if err != nil {
		return interfaces.KnEventList{}, fail(ctx, "event list", err)
	}
	list := interfaces.KnEventList{Entries: make([]interfaces.KnEvent, 0, min(len(events), filter.Limit))}
	if len(events) > filter.Limit {
		events = events[:filter.Limit]
		list.NextCursor = encodeCursor(events[len(events)-1])
	}
	if err := s.attachHandlings(ctx, knID, events); err != nil {
		return interfaces.KnEventList{}, fail(ctx, "event list", err)
	}
	for _, event := range events {
		list.Entries = append(list.Entries, *event)
	}
	return list, nil
}

// attachHandlings gives each event how the platform handled it, if it did.
func (s *service) attachHandlings(ctx context.Context, knID string, events []*interfaces.KnEvent) error {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	handlings, err := s.store.handlingsOf(ctx, knID, ids)
	if err != nil {
		return err
	}
	for _, event := range events {
		event.Handling = handlings[event.ID]
	}
	return nil
}

func (s *service) GetEvent(ctx context.Context, knID, eventID string) (interfaces.KnEvent, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		return interfaces.KnEvent{}, err
	}
	event, found, err := s.store.getEvent(ctx, knID, eventID)
	if err != nil {
		return interfaces.KnEvent{}, fail(ctx, "event lookup", err)
	}
	if !found {
		return interfaces.KnEvent{}, notFound(ctx, berrors.BknBackend_KnEvent_EventNotFound, eventID)
	}
	if event.History, err = s.store.history(ctx, knID, eventID); err != nil {
		return interfaces.KnEvent{}, fail(ctx, "event history", err)
	}
	if err := s.attachHandlings(ctx, knID, []*interfaces.KnEvent{event}); err != nil {
		return interfaces.KnEvent{}, fail(ctx, "event handling", err)
	}
	return *event, nil
}

// checkSpan checks the bounds of the time events occurred: from inclusive,
// to exclusive, 0 for an open end.
func checkSpan(from, to int64) error {
	switch {
	case from < 0 || to < 0:
		return invalidf("from and to must not be negative")
	case from > 0 && to > 0 && from >= to:
		return invalidf("from must be before to")
	}
	return nil
}

// classFor reads an event class as the network runs it, under the head lock,
// so a class with events cannot be deleted while events of it are written.
func classFor(ctx context.Context, w *writer, id, source string) (eventClass, error) {
	class, found, err := w.eventClass(id)
	switch {
	case err != nil:
		return class, err
	case !found:
		return class, notFound(ctx, berrors.BknBackend_KnEvent_EventTypeNotFound, id)
	case class.source != source:
		return class, httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_SourceMismatch).
			WithErrorDetails(map[string]string{"event_type": id, "source": class.source})
	}
	return class, nil
}

func (s *service) ReportEvent(ctx context.Context, knID string, input interfaces.KnEventReportInput) (interfaces.KnEventsResult, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		return interfaces.KnEventsResult{}, err
	}
	if err := validateReportInput(&input); err != nil {
		return interfaces.KnEventsResult{}, fail(ctx, "event report", err)
	}
	o := s.op(ctx)
	var result interfaces.KnEventsResult
	err := s.write(ctx, knID, "event report", func(w *writer) error {
		class, err := classFor(ctx, w, input.EventType, interfaces.KnEventSourceReport)
		if err != nil {
			return err
		}
		var open []*interfaces.KnEvent
		if class.mergeHours > 0 && input.ObjectKey != "" {
			if open, err = w.events(sq.Eq{
				"e.f_event_type": class.id, "e.f_object_key": input.ObjectKey,
				"e.f_status": []string{interfaces.KnEventUnverified, interfaces.KnEventVerified},
			}); err != nil {
				return err
			}
		}
		event, merged, changes := reportEvent(class, input, open, o)
		if err := w.record(changes); err != nil {
			return err
		}
		result.Events = []interfaces.KnEvent{*event}
		if merged != nil {
			result.Events = append(result.Events, *merged)
		}
		return nil
	})
	return result, err
}

func (s *service) ChangeEvent(ctx context.Context, knID, eventID, action string, input interfaces.KnEventReasonInput) (interfaces.KnEventsResult, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		return interfaces.KnEventsResult{}, err
	}
	if err := validateReason(&input); err != nil {
		return interfaces.KnEventsResult{}, fail(ctx, "event change", err)
	}
	o := s.op(ctx)
	var result interfaces.KnEventsResult
	err := s.write(ctx, knID, "event change", func(w *writer) error {
		event, found, err := w.event(eventID)
		if err != nil {
			return err
		}
		if !found {
			return notFound(ctx, berrors.BknBackend_KnEvent_EventNotFound, eventID)
		}
		entry, err := changeEvent(event, action, input.Reason, o)
		if err != nil {
			return err
		}
		if err := w.record([]change{entry}); err != nil {
			return err
		}
		result.Events = []interfaces.KnEvent{*event}
		return nil
	})
	return result, err
}

func (s *service) RecordRuleCheck(ctx context.Context, knID string, input interfaces.KnEventRuleCheckInput) (interfaces.KnEventRuleCheckResult, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	if err := validateRuleCheckInput(&input); err != nil {
		return interfaces.KnEventRuleCheckResult{}, fail(ctx, "rule check", err)
	}
	o := s.op(ctx)
	var result interfaces.KnEventRuleCheckResult
	err := s.write(ctx, knID, "rule check", func(w *writer) error {
		class, err := classFor(ctx, w, input.EventType, interfaces.KnEventSourceRule)
		if err != nil {
			return err
		}
		if err := s.checkRecognition(ctx, w.tx, knID, class.id, input.ActionTypeID); err != nil {
			return err
		}
		result, err = recordCheck(w, class, input, o)
		return err
	})
	return result, err
}

// checkRecognition refuses a check that is not from the class's record-event action type: a class
// that has one in the running version takes checks only from it, and a check that names one must
// name the class's. The action types are read in the write's transaction, after the head lock is
// held; a save of one does not take the lock, so a check sent while the action type is replaced
// may be taken under either.
func (s *service) checkRecognition(ctx context.Context, tx *sql.Tx, knID, classID, actionTypeID string) error {
	list := s.recognitions
	if list == nil {
		list = func(ctx context.Context, tx *sql.Tx, knID, classID string) ([]string, error) {
			return s.store.recognitionActions(ctx, tx, knID, interfaces.MAIN_BRANCH, classID)
		}
	}
	actions, err := list(ctx, tx, knID, classID)
	if err != nil {
		return err
	}
	if slices.Contains(actions, actionTypeID) || len(actions) == 0 && actionTypeID == "" {
		return nil
	}
	return recognitionMismatch(ctx, classID, actionTypeID)
}

func recognitionMismatch(ctx context.Context, classID, actionTypeID string) error {
	return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_RecognitionMismatch).
		WithErrorDetails(map[string]string{"event_type": classID, "action_type_id": actionTypeID})
}

// recordCheck records a check of a rule event class holding the head lock.
func recordCheck(w *writer, class eventClass, input interfaces.KnEventRuleCheckInput, o op) (interfaces.KnEventRuleCheckResult, error) {
	previous, err := w.ruleCheck(class.id)
	if err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	// A rule event's abnormal period stays open until its rule finds it
	// over, whether or not someone handled it; a rejected one never was.
	mine, err := w.events(sq.And{
		sq.Eq{"e.f_event_type": class.id, "e.f_source": interfaces.KnEventSourceRule, "e.f_ended_at": 0},
		sq.NotEq{"e.f_status": interfaces.KnEventRejected},
	})
	if err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	changes, check := applyRuleCheck(class, previous, mine, input, o)
	if err := w.record(changes); err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	if err := w.queueHandlings(handlingsFor(class, changes, input.Hits, o)); err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	if err := w.saveCheck(check); err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	return interfaces.KnEventRuleCheckResult{Events: eventsOf(changes), Check: check}, nil
}

func (s *service) ListRuleChecks(ctx context.Context, knID string) ([]interfaces.KnEventRuleCheck, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		return nil, err
	}
	checks, err := s.store.listChecks(ctx, knID)
	if err != nil {
		return nil, fail(ctx, "rule check list", err)
	}
	return checks, nil
}

// CountEvents counts the occurrences of each event class in a span: the last
// seven days when none is given, up to now when only the start is, and the
// seven days before the end when only the end is.
func (s *service) CountEvents(ctx context.Context, knID string, filter interfaces.KnEventStatsFilter) (interfaces.KnEventStats, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_QUERY_DATA); err != nil {
		return interfaces.KnEventStats{}, err
	}
	if err := checkSpan(filter.From, filter.To); err != nil {
		return interfaces.KnEventStats{}, fail(ctx, "event stats", err)
	}
	// The end is exclusive: an event that occurred now counts. A start left out is the default
	// span before the end, and never before the first moment.
	if filter.To == 0 {
		filter.To = s.now() + 1
	}
	if filter.From == 0 {
		filter.From = max(filter.To-interfaces.KnEventStatsDefaultSpan, 1)
	}
	if err := checkSpan(filter.From, filter.To); err != nil {
		return interfaces.KnEventStats{}, fail(ctx, "event stats", err)
	}
	entries, err := s.store.countEvents(ctx, knID, filter)
	if err != nil {
		return interfaces.KnEventStats{}, fail(ctx, "event stats", err)
	}
	return interfaces.KnEventStats{From: filter.From, To: filter.To, Entries: entries}, nil
}

// ListChanges serves the internal change feed, which carries no caller.
func (s *service) ListChanges(ctx context.Context, knID string, after int64, limit int) (interfaces.KnEventChangeList, error) {
	if err := s.ensureNetwork(ctx, knID); err != nil {
		return interfaces.KnEventChangeList{}, err
	}
	switch {
	case limit <= 0:
		limit = interfaces.KnEventChangeDefaultSize
	case limit > interfaces.KnEventChangeMaxSize:
		limit = interfaces.KnEventChangeMaxSize
	}
	changes, err := s.store.listChanges(ctx, knID, after, limit)
	if err != nil {
		return interfaces.KnEventChangeList{}, fail(ctx, "change feed", err)
	}
	list := interfaces.KnEventChangeList{Entries: changes, LastSeq: after}
	if len(changes) > 0 {
		list.LastSeq = changes[len(changes)-1].Seq
	}
	return list, nil
}
