// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	berrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const (
	// RuleCheckWorker is the name the worker that checks rules on their
	// schedules registers under.
	RuleCheckWorker = "kn-event-rule-checks"

	// ruleCheckPeriod is how often due checks are looked for.
	ruleCheckPeriod = 15 * time.Second
	// ruleChecksPerRound bounds the checks one round starts; the others wait
	// for the next.
	ruleChecksPerRound = 200
	// ruleChecksAtOnce is how many checks run at the same time.
	ruleChecksAtOnce = 4
	// ruleToolWait is how long a check waits for its rule tool.
	ruleToolWait = 90 * time.Second
	// ruleCheckLock lets one replica start the scheduled checks at a time.
	ruleCheckLock = "bkn_kn_event_rule_checks"
	// ruleToolMaxBytes bounds a stored rule tool, whose column is a TEXT.
	ruleToolMaxBytes = 32 << 10
	// replyTextMax bounds a tool's own error that a check repeats.
	replyTextMax = 512
)

// checkLock names the lock a check of an event type holds, so that two
// checks of one type, scheduled or asked for, never overlap.
func checkLock(knID, code string) string { return "bkn_kn_event_check:" + knID + ":" + code }

// checkText is a message a platform check records. Like the event classes, it
// is in the network's one business language, whoever reads it later.
func checkText(id string, data map[string]any, fallback string) string {
	rendered, err := i18n.Render(classLanguage, "BknBackend.KnEvent.Check."+id, data)
	if err != nil {
		logger.Errorf("knowledge-network event: render check text %s: %v", id, err)
		return fallback
	}
	return rendered
}

// Why a rule tool could not be used.
type (
	// toolForbidden is the account refused the tool box.
	toolForbidden struct{ detail string }
	// toolUnavailable is a tool that does not exist, is off, or whose box is
	// not published.
	toolUnavailable struct{ detail string }
	// notCypherTable is a tool that is not a Cypher function of the network
	// returning a table.
	notCypherTable struct{ detail string }
	// toolFailed is a tool that ran and said no: its own status and error.
	toolFailed struct {
		status int
		detail string
	}
	// errChecking is a check of the type that is already running, or a rule
	// that changed while it was checked.
	errChecking struct{ detail string }
)

func (e toolForbidden) Error() string   { return e.detail }
func (e toolUnavailable) Error() string { return e.detail }
func (e notCypherTable) Error() string  { return e.detail }
func (e toolFailed) Error() string {
	return fmt.Sprintf("rule tool answered %d: %s", e.status, e.detail)
}
func (e errChecking) Error() string { return e.detail }

func refused(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// toolError reads a refusal of the execution factory itself.
func toolError(err error, tool interfaces.KnEventRuleTool) error {
	var dependency *interfaces.DependencyError
	if !errors.As(err, &dependency) {
		return err
	}
	switch {
	case refused(dependency.HTTPStatus):
		return toolForbidden{detail: fmt.Sprintf("the account may not use tool box %s", tool.BoxID)}
	case dependency.HTTPStatus == http.StatusBadRequest || dependency.HTTPStatus == http.StatusNotFound:
		return toolUnavailable{detail: fmt.Sprintf("rule tool %s of tool box %s does not exist, is disabled, or its box is not published",
			tool.ToolID, tool.BoxID)}
	}
	return err
}

// sameSchedule reports whether two check schedules are the same setting.
func sameSchedule(a, b *interfaces.KnEventCheckSchedule) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameSubject(a, b *interfaces.KnEventCheckSubject) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.Type == b.Type
}

// sameChecks reports whether a type is checked as it was: same tool, same
// schedule, same subject.
func sameChecks(a, b interfaces.KnEventType) bool {
	return sameRuleTool(a.RuleTool, b.RuleTool) && sameSchedule(a.CheckSchedule, b.CheckSchedule) &&
		sameSubject(a.CheckSubject, b.CheckSubject)
}

// validateChecks checks a type's rule tool and schedule as given and writes
// them as they are stored. It returns the parsed schedule, nil without a rule
// tool.
func validateChecks(input *interfaces.KnEventTypeInput, now time.Time) (*schedule, error) {
	switch {
	case input.RuleTool == nil && input.CheckSchedule == nil:
		return nil, nil
	case input.Source != interfaces.KnEventSourceRule:
		return nil, invalidf("rule_tool and check_schedule are only for rule event types")
	case input.RuleTool == nil:
		return nil, invalidf("check_schedule needs rule_tool")
	case input.CheckSchedule == nil:
		return nil, invalidf("rule_tool needs check_schedule")
	}
	if err := normalizeRuleTool(input.RuleTool); err != nil {
		return nil, err
	}
	if encoded, _ := json.Marshal(input.RuleTool); len(encoded) > ruleToolMaxBytes {
		return nil, tooLarge{detail: fmt.Sprintf("rule_tool exceeds %d bytes", ruleToolMaxBytes)}
	}
	check := input.CheckSchedule
	check.Cron, check.Timezone = strings.TrimSpace(check.Cron), strings.TrimSpace(check.Timezone)
	if check.Timezone == "" {
		check.Timezone = defaultCheckTimezone
	}
	switch {
	case len(check.Cron) > 128:
		return nil, invalidf("check_schedule.cron exceeds 128 characters")
	case len(check.Timezone) > 64:
		return nil, invalidf("check_schedule.timezone exceeds 64 characters")
	}
	parsed, err := parseSchedule(*check, now)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

// fits reports whether a parameter value has the type of the tool's input. A
// check date is a string.
func fits(kind string, raw json.RawMessage, isDate bool) bool {
	if isDate {
		return kind == "" || kind == "string"
	}
	first := raw[0]
	switch kind {
	case "string":
		return first == '"'
	case "number", "integer":
		return first == '-' || (first >= '0' && first <= '9')
	case "boolean":
		return first == 't' || first == 'f'
	case "array":
		return first == '['
	case "object":
		return first == '{'
	}
	return true
}

// matchParameters checks a rule's parameters against the tool's inputs: each
// one an input, of its type, and every required input given.
func matchParameters(tool interfaces.KnEventRuleTool, definition *interfaces.CallerTool) error {
	inputs := make(map[string]interfaces.CallerToolInput, len(definition.Inputs))
	for _, input := range definition.Inputs {
		inputs[input.Name] = input
	}
	for name, raw := range tool.Parameters {
		input, found := inputs[name]
		if !found {
			return invalidf("rule_tool.parameters.%s is not a parameter of the rule tool", name)
		}
		isDate, err := checkDateOf(name, raw)
		if err != nil {
			return err
		}
		if !fits(input.Type, raw, isDate) {
			return invalidf("rule_tool.parameters.%s must be of type %s", name, input.Type)
		}
	}
	for _, input := range definition.Inputs {
		if _, given := tool.Parameters[input.Name]; input.Required && !given {
			return invalidf("rule_tool.parameters.%s is required by the rule tool", input.Name)
		}
	}
	return nil
}

// ruleToolOf reads the rule tool's definition as the account in ctx and
// checks it can be a rule of the network.
func (s *service) ruleToolOf(ctx context.Context, knID string, tool interfaces.KnEventRuleTool) (*interfaces.CallerTool, error) {
	definition, err := s.tools.GetCallerTool(ctx, tool.BoxID, tool.ToolID)
	if err != nil {
		return nil, toolError(err, tool)
	}
	switch {
	case definition.Status != interfaces.EXEC_TOOL_STATUS_ENABLED:
		return nil, toolUnavailable{detail: fmt.Sprintf("rule tool %s is disabled", tool.ToolID)}
	case definition.ScriptType != interfaces.EXEC_FUNCTION_SCRIPT_CYPHER ||
		definition.ResultShape != interfaces.EXEC_FUNCTION_RESULT_TABLE:
		return nil, notCypherTable{detail: fmt.Sprintf("rule tool %s is not a Cypher function returning a table", tool.ToolID)}
	case definition.KnID != knID:
		return nil, notCypherTable{detail: fmt.Sprintf("rule tool %s reads another knowledge network", tool.ToolID)}
	}
	return definition, nil
}

// replyText is a tool's own error, as it said it.
func replyText(reply *interfaces.CallerToolReply) string {
	if reply.Error != "" {
		return clip(reply.Error, replyTextMax)
	}
	var base struct {
		Description  string `json:"description"`
		ErrorDetails any    `json:"error_details"`
	}
	if json.Unmarshal(reply.Body, &base) == nil && base.Description != "" {
		if details, ok := base.ErrorDetails.(string); ok && details != "" {
			return clip(base.Description+": "+details, replyTextMax)
		}
		return clip(base.Description, replyTextMax)
	}
	return clip(string(reply.Body), replyTextMax)
}

// tableOf reads a table function's result.
func tableOf(body json.RawMessage) (ruleTable, error) {
	var reply struct {
		Columns []json.RawMessage `json:"columns"`
		Entries []map[string]any  `json:"entries"`
		Limited bool              `json:"limited"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	// A 19-digit key must not come back rounded.
	decoder.UseNumber()
	if err := decoder.Decode(&reply); err != nil {
		return ruleTable{}, badResult{detail: "the result is not a table"}
	}
	table := ruleTable{Entries: reply.Entries, Limited: reply.Limited}
	for _, raw := range reply.Columns {
		var column struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &column) != nil {
			var name string
			_ = json.Unmarshal(raw, &name)
			column.Name = name
		}
		table.Columns = append(table.Columns, column.Name)
	}
	return table, nil
}

// runRuleTool calls the rule tool as the account in ctx, for a check at at.
func (s *service) runRuleTool(ctx context.Context, tool interfaces.KnEventRuleTool, at time.Time, location *time.Location) (ruleTable, error) {
	arguments, err := arguments(tool, at, location)
	if err != nil {
		return ruleTable{}, err
	}
	reply, err := s.tools.ExecuteCallerTool(ctx, tool.BoxID, tool.ToolID, arguments, ruleToolWait)
	if err != nil {
		return ruleTable{}, toolError(err, tool)
	}
	if reply.StatusCode < http.StatusOK || reply.StatusCode >= http.StatusMultipleChoices {
		return ruleTable{}, toolFailed{status: reply.StatusCode, detail: replyText(reply)}
	}
	return tableOf(reply.Body)
}

// checkInputOf is the check a rule tool's result records.
func checkInputOf(code, version string, table ruleTable) (interfaces.KnEventRuleCheckInput, error) {
	hits, covered, err := ruleHits(table)
	if err != nil {
		return interfaces.KnEventRuleCheckInput{}, err
	}
	input := interfaces.KnEventRuleCheckInput{
		EventType: code, RuleVersion: version, Status: interfaces.KnEventCheckOK, Hits: hits, Covered: covered,
	}
	if table.Limited {
		input.Message = checkText("Limited", nil, "the result was truncated")
	}
	if err := validateRuleCheckInput(&input); err != nil {
		return interfaces.KnEventRuleCheckInput{}, badResult{detail: err.Error()}
	}
	return input, nil
}

// prepareChecks tries a rule tool and schedule being saved, as the caller,
// who checks the rule from then on. It fills in what the platform keeps of
// them; nothing is recorded.
func (s *service) prepareChecks(ctx context.Context, eventType *interfaces.KnEventType, check schedule) error {
	account, _ := ctx.Value(interfaces.ACCOUNT_INFO_KEY).(interfaces.AccountInfo)
	if account.ID == "" {
		return invalidf("rule_tool is checked as the person who saves it, and the request has no account")
	}
	now := time.UnixMilli(s.now())
	tool := *eventType.RuleTool
	definition, err := s.ruleToolOf(ctx, eventType.KNID, tool)
	if err != nil {
		return err
	}
	if err := matchParameters(tool, definition); err != nil {
		return err
	}
	table, err := s.runRuleTool(ctx, tool, now, check.location)
	if err != nil {
		return err
	}
	input, err := checkInputOf(eventType.Code, "", table)
	if err != nil {
		return err
	}
	actor := s.actor(ctx)
	eventType.CheckSubject = &interfaces.KnEventCheckSubject{ID: actor.ID, Type: actor.Type, Name: actor.Name}
	eventType.RuleVersion = ruleVersion(tool, definition.UpdateTime)
	eventType.NextCheckAt = millis(check.next(now))
	eventType.CheckFailures = 0
	eventType.Trial = &interfaces.KnEventRuleTrial{Hits: len(input.Hits), Limited: table.Limited}
	return nil
}

func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// checkMessage is what a check that could not decide records.
func checkMessage(err error) string {
	var forbidden toolForbidden
	var failed toolFailed
	var unavailable toolUnavailable
	var notTable notCypherTable
	var bad badResult
	var invalid invalidInput
	switch {
	case errors.As(err, &forbidden), errors.As(err, &failed) && refused(failed.status):
		return checkText("NoPermission", nil, err.Error())
	case errors.As(err, &unavailable), errors.As(err, &notTable):
		return checkText("ToolUnavailable", nil, err.Error())
	case errors.As(err, &bad):
		return bad.message()
	case errors.As(err, &failed):
		return checkText("Failed", map[string]any{"error": failed.detail}, err.Error())
	case errors.As(err, &invalid):
		return checkText("Failed", map[string]any{"error": invalid.detail}, err.Error())
	}
	return checkText("Failed", map[string]any{"error": err.Error()}, err.Error())
}

// subjectContext is ctx as the type's check subject, in the network's language.
func subjectContext(ctx context.Context, subject interfaces.KnEventCheckSubject) context.Context {
	ctx = context.WithValue(ctx, interfaces.ACCOUNT_INFO_KEY, interfaces.AccountInfo{ID: subject.ID, Type: subject.Type})
	return context.WithValue(ctx, i18n.LanguageKey, classLanguage)
}

// runCheck calls a type's rule tool as its check subject and reads what it
// found. A check that cannot decide says why.
func (s *service) runCheck(ctx context.Context, eventType interfaces.KnEventType, at time.Time) interfaces.KnEventRuleCheckInput {
	tool := *eventType.RuleTool
	version := eventType.RuleVersion
	input, err := func() (interfaces.KnEventRuleCheckInput, error) {
		check, err := parseSchedule(*eventType.CheckSchedule, at)
		if err != nil {
			return interfaces.KnEventRuleCheckInput{}, err
		}
		definition, err := s.ruleToolOf(ctx, eventType.KNID, tool)
		if err != nil {
			return interfaces.KnEventRuleCheckInput{}, err
		}
		version = ruleVersion(tool, definition.UpdateTime)
		if err := matchParameters(tool, definition); err != nil {
			return interfaces.KnEventRuleCheckInput{}, err
		}
		table, err := s.runRuleTool(ctx, tool, at, check.location)
		if err != nil {
			return interfaces.KnEventRuleCheckInput{}, err
		}
		return checkInputOf(eventType.Code, version, table)
	}()
	if err != nil {
		logger.Warnf("knowledge-network event: rule check of %s in network %s could not decide: %v",
			eventType.Code, eventType.KNID, err)
		return interfaces.KnEventRuleCheckInput{
			EventType: eventType.Code, RuleVersion: version, Status: interfaces.KnEventCheckUnknown,
			Message: clip(checkMessage(err), messageMax),
		}
	}
	return input
}

// checkRule checks a rule event type with its rule tool, as its check
// subject, and records what the tool found. A scheduled check also sets when
// the type is checked next; one asked for leaves that as it is.
func (s *service) checkRule(ctx context.Context, knID, code string, scheduled bool) (interfaces.KnEventRuleCheckResult, error) {
	var result interfaces.KnEventRuleCheckResult
	ran, err := s.store.alone(ctx, checkLock(knID, code), func() error {
		eventType, found, err := s.store.getType(ctx, knID, code)
		switch {
		case err != nil:
			return err
		case !found:
			return notFound(ctx, berrors.BknBackend_KnEvent_EventTypeNotFound, code)
		case eventType.RuleTool == nil || eventType.CheckSchedule == nil || eventType.CheckSubject == nil:
			if scheduled {
				return nil
			}
			return invalidf("event type %s has no rule_tool: its rule is checked by the applications", code)
		case scheduled && (eventType.NextCheckAt == 0 || eventType.NextCheckAt > s.now()):
			// Saved again since it was found due.
			return nil
		}
		started := time.Now()
		subject := subjectContext(ctx, *eventType.CheckSubject)
		input := s.runCheck(subject, eventType, time.UnixMilli(s.now()))
		result, err = s.recordPlatformCheck(subject, eventType, input, scheduled)
		if err == nil {
			logger.Infof("knowledge-network event: checked rule %s of network %s as %s %s in %s: %s, %d hits, %s",
				code, knID, eventType.CheckSubject.Type, eventType.CheckSubject.ID, time.Since(started).Round(time.Millisecond),
				input.Status, len(input.Hits), input.Message)
		}
		return err
	})
	if err != nil {
		return result, fail(ctx, "rule check", err)
	}
	if !ran {
		return result, fail(ctx, "rule check", errChecking{detail: fmt.Sprintf("event type %s is being checked", code)})
	}
	return result, nil
}

// recordPlatformCheck records a platform check as a rule check of its
// subject, and keeps the type's version, failures and next check time.
func (s *service) recordPlatformCheck(ctx context.Context, checked interfaces.KnEventType,
	input interfaces.KnEventRuleCheckInput, scheduled bool) (interfaces.KnEventRuleCheckResult, error) {
	o := s.op(ctx)
	var result interfaces.KnEventRuleCheckResult
	err := s.store.write(ctx, checked.KNID, func(w *writer) error {
		if err := s.ensureNetwork(ctx, checked.KNID); err != nil {
			return err
		}
		current, err := typeFor(ctx, w, checked.Code, interfaces.KnEventSourceRule)
		if err != nil {
			return err
		}
		if !sameChecks(current, checked) {
			// What was checked is not the rule any more; the new one is
			// checked on its own.
			return errChecking{detail: fmt.Sprintf("the rule of event type %s changed during the check", checked.Code)}
		}
		if result, err = recordCheck(w, current, input, o); err != nil {
			return err
		}
		failures, next := 0, current.NextCheckAt
		if input.Status == interfaces.KnEventCheckUnknown {
			failures = current.CheckFailures + 1
		}
		if scheduled {
			now := time.UnixMilli(s.now())
			check, err := parseSchedule(*current.CheckSchedule, now)
			switch {
			case err != nil:
				logger.Errorf("knowledge-network event: schedule of %s in network %s no longer reads, checks stop: %v",
					current.Code, current.KNID, err)
				next = 0
			case failures == 0:
				next = millis(check.next(now))
			default:
				next = millis(retryAt(check, now, failures))
			}
		}
		return w.setCheckState(current.Code, input.RuleVersion, failures, next)
	})
	return result, err
}

// CheckEventType checks a rule now, as the scheduled checks do: as the type's
// check subject, not the caller.
func (s *service) CheckEventType(ctx context.Context, knID, code string) (interfaces.KnEventRuleCheckResult, error) {
	if err := s.open(ctx, knID, interfaces.OPERATION_TYPE_MODIFY); err != nil {
		return interfaces.KnEventRuleCheckResult{}, err
	}
	return s.checkRule(ctx, knID, code, false)
}

// ruleChecker starts the checks that are due, on one replica at a time.
type ruleChecker struct {
	service *service
}

func newRuleChecker(service *service) *ruleChecker { return &ruleChecker{service: service} }

// run works every period until ctx ends.
func (c *ruleChecker) run(ctx context.Context) error {
	ticker := time.NewTicker(ruleCheckPeriod)
	defer ticker.Stop()
	for {
		c.round(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// round runs the checks that are due, unless another replica is at it. A
// time that passed while nothing ran is not made up for: a check sets the
// next time from when it ends.
func (c *ruleChecker) round(ctx context.Context) {
	_, err := c.service.store.alone(ctx, ruleCheckLock, func() error {
		due, err := c.service.store.dueTypes(ctx, c.service.now(), ruleChecksPerRound)
		if err != nil {
			return err
		}
		slots := make(chan struct{}, ruleChecksAtOnce)
		var wg sync.WaitGroup
		for _, eventType := range due {
			slots <- struct{}{}
			wg.Add(1)
			go func(knID, code string) {
				defer func() { <-slots; wg.Done() }()
				if _, err := c.service.checkRule(ctx, knID, code, true); err != nil && ctx.Err() == nil {
					logger.Errorf("knowledge-network event: scheduled check of %s in network %s: %v", code, knID, err)
				}
			}(eventType.KNID, eventType.Code)
		}
		wg.Wait()
		return nil
	})
	if err != nil && ctx.Err() == nil {
		logger.Errorf("knowledge-network event rule checks: %v", err)
	}
}
