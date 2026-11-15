// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/drivenadapters/agent_operator"
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
	store *store
	// classes makes the object type an event type's events are read as.
	classes     *classes
	permissions permissionChecker
	networks    networkChecker
	names       nameResolver
	// tools calls rule tools as the account in the context.
	tools interfaces.CallerToolAccess
	now   func() int64
	newID func() string
}

func newService(appSetting *common.AppSetting, store *store, classes *classes) *service {
	return &service{
		store:       store,
		classes:     classes,
		permissions: permission.NewPermissionService(appSetting),
		networks:    knowledge_network.NewKNService(appSetting),
		names:       user_mgmt.NewUserMgmtService(appSetting),
		tools:       agent_operator.NewCallerToolAccess(appSetting),
		now:         func() int64 { return time.Now().UnixMilli() },
		newID:       newEventID,
	}
}

func newEventID() string {
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

var _ interfaces.KnEventServiceV1 = (*service)(nil)

// fail turns an error into the HTTP error the caller sees.
func fail(ctx context.Context, operation string, err error) error {
	var httpErr *httperrors.HTTPError
	var invalid invalidInput
	var large tooLarge
	var notAllowed errActionNotAllowed
	var noReason errReasonRequired
	var forbidden toolForbidden
	var unavailable toolUnavailable
	var notTable notCypherTable
	var failed toolFailed
	var bad badResult
	var checking errChecking
	switch {
	case errors.As(err, &httpErr):
		return err
	case errors.As(err, &forbidden):
		return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).WithErrorDetails(forbidden.detail)
	case errors.As(err, &unavailable):
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnEvent_RuleToolUnavailable).WithErrorDetails(unavailable.detail)
	case errors.As(err, &notTable):
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnEvent_RuleToolNotCypherTable).WithErrorDetails(notTable.detail)
	case errors.As(err, &failed):
		// The tool's own answer, as it gave it.
		details := map[string]any{"status": failed.status, "error": failed.detail}
		if refused(failed.status) {
			return httperrors.NewHTTPError(ctx, http.StatusForbidden, httperrors.PublicError_Forbidden).WithErrorDetails(details)
		}
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnEvent_InvalidParameter).WithErrorDetails(details)
	case errors.As(err, &bad):
		return httperrors.NewHTTPError(ctx, http.StatusBadRequest, berrors.BknBackend_KnEvent_InvalidParameter).
			WithErrorDetails("the rule tool's result: " + bad.detail)
	case errors.As(err, &checking):
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_RuleChecking).WithErrorDetails(checking.detail)
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
		now:   s.now(),
		actor: s.actor(ctx),
		newID: s.newID,
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

// Event types.

func (s *service) ListEventTypes(ctx context.Context, knID string) ([]interfaces.KnEventType, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		return nil, err
	}
	types, err := s.store.listTypes(ctx, knID)
	if err != nil {
		return nil, fail(ctx, "type list", err)
	}
	s.nameSubjects(ctx, types)
	return types, nil
}

// nameSubjects gives the types' check subjects their display names. A
// directory outage leaves them empty.
func (s *service) nameSubjects(ctx context.Context, types []interfaces.KnEventType) {
	var accounts []*interfaces.AccountInfo
	for _, eventType := range types {
		if subject := eventType.CheckSubject; subject != nil {
			accounts = append(accounts, &interfaces.AccountInfo{ID: subject.ID, Type: subject.Type})
		}
	}
	if len(accounts) == 0 || s.names == nil {
		return
	}
	if err := s.names.GetAccountNames(ctx, accounts); err != nil {
		logger.Warnf("knowledge-network event: resolve the names of check subjects: %v", err)
		return
	}
	next := 0
	for _, eventType := range types {
		if subject := eventType.CheckSubject; subject != nil {
			if name := accounts[next].Name; name != "-" {
				subject.Name = name
			}
			next++
		}
	}
}

func (s *service) GetEventType(ctx context.Context, knID, code string) (interfaces.KnEventType, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_VIEW_DETAIL); err != nil {
		return interfaces.KnEventType{}, err
	}
	eventType, found, err := s.store.getType(ctx, knID, code)
	if err != nil {
		return interfaces.KnEventType{}, fail(ctx, "type lookup", err)
	}
	if !found {
		return interfaces.KnEventType{}, notFound(ctx, berrors.BknBackend_KnEvent_EventTypeNotFound, code)
	}
	s.nameSubjects(ctx, []interfaces.KnEventType{eventType})
	return eventType, nil
}

func (s *service) CreateEventType(ctx context.Context, knID string, input interfaces.KnEventTypeInput) (interfaces.KnEventType, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return interfaces.KnEventType{}, err
	}
	if !eventTypeCodePattern.MatchString(input.Code) {
		return interfaces.KnEventType{}, fail(ctx, "type create", invalidf("code must match %s", eventTypeCodePattern))
	}
	if err := validateEventTypeInput(&input); err != nil {
		return interfaces.KnEventType{}, fail(ctx, "type create", err)
	}
	check, err := validateChecks(&input, time.UnixMilli(s.now()))
	if err != nil {
		return interfaces.KnEventType{}, fail(ctx, "type create", err)
	}
	actor, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	now := s.now()
	eventType := interfaces.KnEventType{
		KNID: knID, Code: input.Code, Name: input.Name, Source: input.Source, Subject: input.Subject,
		Severity: input.Severity, MergeHours: input.MergeHours, Rule: input.Rule, RuleVersion: input.RuleVersion,
		RuleTool: input.RuleTool, CheckSchedule: input.CheckSchedule,
		Creator: actor.ID, CreatorType: actor.Type, CreateTime: now,
		Updater: actor.ID, UpdaterType: actor.Type, UpdateTime: now,
	}
	// The rule tool is tried before anything is made: a rule that cannot run
	// is not saved.
	if check != nil {
		if err := s.prepareChecks(ctx, &eventType, *check); err != nil {
			return interfaces.KnEventType{}, fail(ctx, "type create", err)
		}
	}
	taken := func() error {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_EventTypeExisted).
			WithErrorDetails(input.Code)
	}
	// The class comes first: a kind of event that cannot be an object type of
	// the network is not created. A code that is taken, or whose class is
	// another type's, is refused before, so that the class of the type that
	// has it is left as it is.
	err = s.store.exclusive(ctx, classLock(knID), func() error {
		if _, found, err := s.store.getType(ctx, knID, input.Code); err != nil {
			return err
		} else if found {
			return taken()
		}
		if err := claim(ctx, s.store, knID, input.Code, true); err != nil {
			return err
		}
		class, err := s.classes.ensure(ctx, eventType)
		if err == nil {
			eventType.ObjectTypeID, eventType.DatasetID = class.objectTypeID, class.datasetID
			err = s.createType(ctx, eventType, class, taken)
		}
		if err != nil {
			s.classes.discard(ctx, eventType, class)
		}
		return err
	})
	if err != nil {
		return interfaces.KnEventType{}, fail(ctx, "type create", err)
	}
	return eventType, nil
}

// createType stores a new event type with the class made for it. It holds the
// head lock, like event writes: a network deletion then cannot miss the new
// type and leave it behind.
func (s *service) createType(ctx context.Context, eventType interfaces.KnEventType, class eventClass, taken func() error) error {
	return s.write(ctx, eventType.KNID, "type create", func(w *writer) error {
		if _, found, err := w.eventType(eventType.Code); err != nil {
			return err
		} else if found {
			return taken()
		}
		// No event of the type exists before the type does.
		if err := s.classes.clear(ctx, eventType, class); err != nil {
			return err
		}
		created, err := w.createType(eventType)
		if err != nil {
			return err
		}
		if !created {
			return taken()
		}
		return nil
	})
}

// updated is an event type with an update applied to what an update may
// change. Code and source are fixed once the type exists; the body may
// repeat them. A new rule tool or schedule comes back as changed: it is
// tried, and checked as the caller from then on. Otherwise the type is
// checked as it was.
func updated(current interfaces.KnEventType, input interfaces.KnEventTypeInput, now time.Time) (interfaces.KnEventType, *schedule, error) {
	if input.Code != "" && input.Code != current.Code {
		return current, nil, invalidf("code cannot change")
	}
	if input.Source == "" {
		input.Source = current.Source
	}
	if input.Source != current.Source {
		return current, nil, invalidf("source cannot change")
	}
	if err := validateEventTypeInput(&input); err != nil {
		return current, nil, err
	}
	check, err := validateChecks(&input, now)
	if err != nil {
		return current, nil, err
	}
	same := sameRuleTool(current.RuleTool, input.RuleTool) && sameSchedule(current.CheckSchedule, input.CheckSchedule)
	current.Name, current.Subject, current.Severity = input.Name, input.Subject, input.Severity
	current.MergeHours, current.Rule = input.MergeHours, input.Rule
	switch {
	case input.RuleTool == nil:
		// Checked by the applications again, with the version they give.
		current.RuleVersion = input.RuleVersion
		current.RuleTool, current.CheckSchedule, current.CheckSubject = nil, nil, nil
		current.NextCheckAt, current.CheckFailures = 0, 0
	case !same:
		current.RuleTool, current.CheckSchedule = input.RuleTool, input.CheckSchedule
		return current, check, nil
	}
	return current, nil, nil
}

func (s *service) UpdateEventType(ctx context.Context, knID, code string, input interfaces.KnEventTypeInput) (interfaces.KnEventType, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return interfaces.KnEventType{}, err
	}
	// The rule tool is tried before the class lock is taken, as at creation: a
	// try may take as long as the tool may run, far longer than other changes
	// to the network's types wait for that lock.
	tried, err := s.tryUpdate(ctx, knID, code, input)
	if err != nil {
		return interfaces.KnEventType{}, fail(ctx, "type update", err)
	}
	var eventType interfaces.KnEventType
	err = s.store.exclusive(ctx, classLock(knID), func() error {
		var err error
		eventType, err = s.updateType(ctx, knID, code, input, tried)
		return err
	})
	if err != nil {
		return interfaces.KnEventType{}, fail(ctx, "type update", err)
	}
	return eventType, nil
}

// triedUpdate is an update with its rule checks prepared before it is made.
type triedUpdate struct {
	// from is the type the update was tried on. It is made only to a type that
	// still has the same rule tool and schedule.
	from interfaces.KnEventType
	// checks is the type with its checks prepared, nil for an update that
	// keeps the checks as they are.
	checks *interfaces.KnEventType
}

// tryUpdate tries the rule tool of an update that changes the tool or the
// schedule, as the caller, before anything changes.
func (s *service) tryUpdate(ctx context.Context, knID, code string, input interfaces.KnEventTypeInput) (triedUpdate, error) {
	before, found, err := s.store.getType(ctx, knID, code)
	if err != nil {
		return triedUpdate{}, err
	}
	if !found {
		return triedUpdate{}, notFound(ctx, berrors.BknBackend_KnEvent_EventTypeNotFound, code)
	}
	after, check, err := updated(before, input, time.UnixMilli(s.now()))
	if err != nil {
		return triedUpdate{}, err
	}
	tried := triedUpdate{from: before}
	if check != nil {
		if err := s.prepareChecks(ctx, &after, *check); err != nil {
			return triedUpdate{}, err
		}
		tried.checks = &after
	}
	return tried, nil
}

// updateType updates an event type holding the network's class lock.
func (s *service) updateType(ctx context.Context, knID, code string, input interfaces.KnEventTypeInput, tried triedUpdate) (interfaces.KnEventType, error) {
	actor, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	// The class has the type's name and a relation to its subject, so it is
	// written again when either changes, and made for a type that has none:
	// one from before events were object types. As at creation it comes
	// first, outside the head lock.
	before, found, err := s.store.getType(ctx, knID, code)
	if err != nil {
		return interfaces.KnEventType{}, err
	}
	if !found {
		return interfaces.KnEventType{}, notFound(ctx, berrors.BknBackend_KnEvent_EventTypeNotFound, code)
	}
	after, _, err := updated(before, input, time.UnixMilli(s.now()))
	if err != nil {
		return interfaces.KnEventType{}, err
	}
	var class *eventClass
	if before.ObjectTypeID == "" || after.Name != before.Name || after.Subject != before.Subject {
		if before.ObjectTypeID == "" {
			if err := claim(ctx, s.store, knID, code, false); err != nil {
				return interfaces.KnEventType{}, err
			}
		}
		made, err := s.classes.ensure(ctx, after)
		if err != nil {
			return interfaces.KnEventType{}, err
		}
		class = &made
	}
	var eventType interfaces.KnEventType
	// Under the head lock, like creation, so the update reads the type it changes.
	err = s.write(ctx, knID, "type update", func(w *writer) error {
		current, found, err := w.eventType(code)
		if err != nil {
			return err
		}
		if !found {
			return notFound(ctx, berrors.BknBackend_KnEvent_EventTypeNotFound, code)
		}
		var changed *schedule
		if eventType, changed, err = updated(current, input, time.UnixMilli(s.now())); err != nil {
			return err
		}
		// The try was decided on the tool and schedule the type had then. A type
		// given another tool or schedule since was not tried this way.
		if !sameRuleTool(tried.from.RuleTool, current.RuleTool) || !sameSchedule(tried.from.CheckSchedule, current.CheckSchedule) ||
			(changed != nil) != (tried.checks != nil) {
			return errChecking{detail: "the event type's rule tool or check schedule changed during the save; save again"}
		}
		if changed != nil {
			prepared := tried.checks
			eventType.RuleVersion, eventType.CheckSubject = prepared.RuleVersion, prepared.CheckSubject
			eventType.NextCheckAt, eventType.CheckFailures, eventType.Trial = prepared.NextCheckAt, 0, prepared.Trial
		}
		if class != nil {
			// Nothing has been written to a dataset the type does not have yet.
			if current.DatasetID != class.datasetID {
				if err := s.classes.clear(ctx, eventType, *class); err != nil {
					return err
				}
			}
			eventType.ObjectTypeID, eventType.DatasetID = class.objectTypeID, class.datasetID
		}
		eventType.Updater, eventType.UpdaterType, eventType.UpdateTime = actor.ID, actor.Type, s.now()
		return w.updateType(eventType)
	})
	if err != nil {
		return interfaces.KnEventType{}, err
	}
	return eventType, nil
}

func (s *service) DeleteEventType(ctx context.Context, knID, code string) error {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return err
	}
	inUse := func() error {
		return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_EventTypeInUse).WithErrorDetails(code)
	}
	var deleted interfaces.KnEventType
	var versioned bool
	err := s.store.exclusive(ctx, classLock(knID), func() error {
		var class string
		var changed bool
		// Under the head lock no event of the type can be written meanwhile, so
		// the class goes only with a type that has no events, and before it: a
		// class left without its type would stay in the network.
		err := s.write(ctx, knID, "type delete", func(w *writer) error {
			current, found, err := w.eventType(code)
			if err != nil {
				return err
			}
			if !found {
				return notFound(ctx, berrors.BknBackend_KnEvent_EventTypeNotFound, code)
			}
			if used, err := w.hasEvents(code); err != nil || used {
				if err == nil {
					err = inUse()
				}
				return err
			}
			class = current.ObjectTypeID
			if versioned, changed, err = s.classes.remove(ctx, current); err != nil {
				return err
			}
			if _, used, err := w.deleteType(code); err != nil || used {
				if err == nil {
					err = inUse()
				}
				return err
			}
			deleted = current
			return nil
		})
		if err != nil && changed {
			// The type is still there and its class may be gone. Left without
			// one, it is given its class again.
			if forgetErr := s.store.forgetClass(context.WithoutCancel(ctx), knID, code, class); forgetErr != nil {
				logger.Errorf("knowledge-network event: event type %s of network %s may have lost class %s: %v",
					code, knID, class, forgetErr)
			}
		}
		return err
	})
	if err != nil {
		return fail(ctx, "type delete", err)
	}
	// The dataset goes after the type, which nothing writes to any more. One
	// that is left is cleared when the type is made again.
	if err := s.classes.drop(ctx, deleted, versioned); err != nil {
		logger.Warnf("knowledge-network event: delete dataset %s of deleted event type %s of network %s: %v",
			deleted.DatasetID, code, knID, err)
	}
	return nil
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
	for _, event := range events {
		list.Entries = append(list.Entries, *event)
	}
	return list, nil
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
	return *event, nil
}

// typeFor reads an event type under the head lock, so it cannot be deleted
// while events of it are written.
func typeFor(ctx context.Context, w *writer, code, source string) (interfaces.KnEventType, error) {
	eventType, found, err := w.eventType(code)
	switch {
	case err != nil:
		return eventType, err
	case !found:
		return eventType, notFound(ctx, berrors.BknBackend_KnEvent_EventTypeNotFound, code)
	case eventType.Source != source:
		return eventType, httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_SourceMismatch).
			WithErrorDetails(map[string]string{"event_type": code, "source": eventType.Source})
	}
	return eventType, nil
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
		eventType, err := typeFor(ctx, w, input.EventType, interfaces.KnEventSourceReport)
		if err != nil {
			return err
		}
		var open []*interfaces.KnEvent
		if eventType.MergeHours > 0 && input.ObjectKey != "" {
			if open, err = w.events(sq.Eq{
				"e.f_event_type": eventType.Code, "e.f_object_key": input.ObjectKey,
				"e.f_status": []string{interfaces.KnEventUnverified, interfaces.KnEventVerified},
			}); err != nil {
				return err
			}
		}
		event, merged, changes := reportEvent(eventType, input, open, o)
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
		eventType, err := typeFor(ctx, w, input.EventType, interfaces.KnEventSourceRule)
		if err != nil {
			return err
		}
		// Checked by the platform: an application's check would be a second
		// opinion on the same rule.
		if eventType.RuleTool != nil {
			return httperrors.NewHTTPError(ctx, http.StatusConflict, berrors.BknBackend_KnEvent_CheckedByPlatform).
				WithErrorDetails(eventType.Code)
		}
		result, err = recordCheck(w, eventType, input, o)
		return err
	})
	return result, err
}

// recordCheck records a check of a rule event type holding the head lock.
func recordCheck(w *writer, eventType interfaces.KnEventType, input interfaces.KnEventRuleCheckInput, o op) (interfaces.KnEventRuleCheckResult, error) {
	previous, err := w.ruleCheck(eventType.Code)
	if err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	// A rule event's abnormal period stays open until its rule finds it
	// over, whether or not someone handled it; a rejected one never was.
	mine, err := w.events(sq.And{
		sq.Eq{"e.f_event_type": eventType.Code, "e.f_source": interfaces.KnEventSourceRule, "e.f_ended_at": 0},
		sq.NotEq{"e.f_status": interfaces.KnEventRejected},
	})
	if err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	changes, check := applyRuleCheck(eventType, previous, mine, input, o)
	if err := w.record(changes); err != nil {
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
