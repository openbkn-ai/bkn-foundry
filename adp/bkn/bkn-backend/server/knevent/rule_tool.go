// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

// A rule event type with a rule tool is checked by the platform: on its
// schedule the module calls the tool, a Cypher function of the network, as
// the person who saved the rule, and records what it finds as a rule check.

const (
	// defaultCheckTimezone is the time zone of a schedule that names none.
	defaultCheckTimezone = "Asia/Shanghai"
	// minCheckInterval is the shortest time allowed between two checks.
	minCheckInterval = 5 * time.Minute
	// scheduleProbes is how many coming check times are looked at for the
	// shortest interval. A schedule that fires every five minutes covers a
	// week; a yearly one, centuries.
	scheduleProbes = 2000
	// ruleVersionLength is how many hex digits of the hash a computed rule
	// version keeps.
	ruleVersionLength = 12
	// checkDate is the value of a check_time parameter that stands for the
	// check's date.
	checkDate = "date"
)

// Columns a rule tool returns. Every other column is an event attribute.
const (
	keyColumn  = "key"
	nameColumn = "name"
)

var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// schedule is a parsed check schedule.
type schedule struct {
	spec     cron.Schedule
	location *time.Location
}

// next is the first check time after t; zero when the schedule never fires.
func (s schedule) next(t time.Time) time.Time {
	return s.spec.Next(t.In(s.location))
}

// parseSchedule reads a check schedule, refusing one that would check more
// often than every five minutes, as seen from now.
func parseSchedule(input interfaces.KnEventCheckSchedule, now time.Time) (schedule, error) {
	text := strings.TrimSpace(input.Cron)
	if text == "" {
		return schedule{}, invalidf("check_schedule.cron is required")
	}
	if strings.HasPrefix(text, "TZ=") || strings.HasPrefix(text, "CRON_TZ=") {
		return schedule{}, invalidf("check_schedule.cron takes no time zone; set check_schedule.timezone")
	}
	spec, err := cronParser.Parse(text)
	if err != nil {
		return schedule{}, invalidf("check_schedule.cron is not a five-field cron expression: %v", err)
	}
	zone := input.Timezone
	if zone == "" {
		zone = defaultCheckTimezone
	}
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "Local" {
		return schedule{}, invalidf("check_schedule.timezone %q is not an IANA time zone", input.Timezone)
	}
	s := schedule{spec: spec, location: location}
	at := s.next(now)
	if at.IsZero() {
		return schedule{}, invalidf("check_schedule.cron never fires")
	}
	for range scheduleProbes {
		following := s.next(at)
		if following.IsZero() {
			break
		}
		if following.Sub(at) < minCheckInterval {
			return schedule{}, invalidf("check_schedule.cron checks more often than every %d minutes", int(minCheckInterval/time.Minute))
		}
		at = following
	}
	return s, nil
}

// retryAt is when a check that failed for the failures-th time in a row is
// tried again: after 1, 2, 4, then 8 minutes, and never later than the next
// check time.
func retryAt(s schedule, now time.Time, failures int) time.Time {
	wait := time.Minute << min(max(failures-1, 0), 3)
	retry := now.Add(wait)
	if next := s.next(now); !next.IsZero() && next.Before(retry) {
		return next
	}
	return retry
}

// checkDateOf reports whether a parameter value stands for the check's date,
// and refuses a check_time that is not one the module knows.
func checkDateOf(name string, raw json.RawMessage) (bool, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return false, nil
	}
	value, found := object["check_time"]
	if !found {
		return false, nil
	}
	var kind string
	if len(object) != 1 || json.Unmarshal(value, &kind) != nil || kind != checkDate {
		return false, invalidf(`rule_tool.parameters.%s: check_time takes only "date"`, name)
	}
	return true, nil
}

// normalizeRuleTool checks a rule tool's shape and writes its parameters
// compactly, so that equal settings compare and hash equal.
func normalizeRuleTool(tool *interfaces.KnEventRuleTool) error {
	tool.BoxID, tool.ToolID = strings.TrimSpace(tool.BoxID), strings.TrimSpace(tool.ToolID)
	if tool.BoxID == "" || tool.ToolID == "" {
		return invalidf("rule_tool.box_id and rule_tool.tool_id are required")
	}
	if tool.Parameters == nil {
		tool.Parameters = map[string]json.RawMessage{}
	}
	for name, raw := range tool.Parameters {
		var compact bytes.Buffer
		if err := json.Compact(&compact, raw); err != nil {
			return invalidf("rule_tool.parameters.%s is not JSON", name)
		}
		if bytes.Equal(compact.Bytes(), []byte("null")) {
			return invalidf("rule_tool.parameters.%s has no value", name)
		}
		if _, err := checkDateOf(name, compact.Bytes()); err != nil {
			return err
		}
		tool.Parameters[name] = compact.Bytes()
	}
	return nil
}

// sameRuleTool reports whether two rule tools are the same setting.
func sameRuleTool(a, b *interfaces.KnEventRuleTool) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.BoxID != b.BoxID || a.ToolID != b.ToolID || len(a.Parameters) != len(b.Parameters) {
		return false
	}
	for name, value := range a.Parameters {
		if other, found := b.Parameters[name]; !found || !bytes.Equal(value, other) {
			return false
		}
	}
	return true
}

// arguments is what the tool is called with at a check time: the check date
// in the schedule's time zone, the other parameters as they are set.
func arguments(tool interfaces.KnEventRuleTool, at time.Time, location *time.Location) (map[string]any, error) {
	values := make(map[string]any, len(tool.Parameters))
	for name, raw := range tool.Parameters {
		isDate, err := checkDateOf(name, raw)
		if err != nil {
			return nil, err
		}
		if isDate {
			values[name] = at.In(location).Format(time.DateOnly)
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, invalidf("rule_tool.parameters.%s is not JSON", name)
		}
		values[name] = value
	}
	return values, nil
}

// ruleVersion identifies what a rule decides by: the tool, when the tool was
// last changed, and the parameters as set. The check date is not part of it.
func ruleVersion(tool interfaces.KnEventRuleTool, toolUpdated int64) string {
	names := make([]string, 0, len(tool.Parameters))
	for name := range tool.Parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%d", tool.BoxID, tool.ToolID, toolUpdated)
	for _, name := range names {
		_, _ = fmt.Fprintf(hash, "\x00%s=%s", name, tool.Parameters[name])
	}
	return hex.EncodeToString(hash.Sum(nil))[:ruleVersionLength]
}

// ruleTable is a rule tool's result: a table whose rows are the objects the
// rule finds now.
type ruleTable struct {
	Columns []string
	Entries []map[string]any
	Limited bool
}

// badResult is a result a check cannot decide by. Its detail answers a save;
// a check records the message named by id, in the network's language.
type badResult struct {
	detail string
	id     string
	data   map[string]any
}

func (e badResult) Error() string { return e.detail }

func (e badResult) message() string {
	if e.id == "" {
		return checkText("BadResult", map[string]any{"detail": e.detail}, e.detail)
	}
	return checkText(e.id, e.data, e.detail)
}

// scalarText writes a scalar result value as text; ok is false for a value
// that is not a string or a number.
func scalarText(value any) (text string, ok bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), true
	case int64:
		return strconv.FormatInt(v, 10), true
	case int:
		return strconv.Itoa(v), true
	}
	return "", false
}

// attributeText writes any other column's value as text: JSON for one that is
// not a string or a number.
func attributeText(value any) string {
	if text, ok := scalarText(value); ok {
		return text
	}
	if b, ok := value.(bool); ok {
		return strconv.FormatBool(b)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

// ruleHits reads the objects a rule found from its result. A row without a key
// is skipped, and a key found again keeps its first row. A truncated result
// covers only the keys it returned.
func ruleHits(table ruleTable) (hits []interfaces.KnEventRuleHit, covered []string, err error) {
	hasKey := false
	for _, column := range table.Columns {
		if column == keyColumn {
			hasKey = true
		}
	}
	if !hasKey && len(table.Columns) == 0 && len(table.Entries) > 0 {
		_, hasKey = table.Entries[0][keyColumn]
	}
	if !hasKey {
		return nil, nil, badResult{detail: "the result has no key column", id: "NoKeyColumn"}
	}
	hits = []interfaces.KnEventRuleHit{}
	seen := make(map[string]bool, len(table.Entries))
	for index, entry := range table.Entries {
		raw := entry[keyColumn]
		if raw == nil {
			continue
		}
		key, ok := scalarText(raw)
		if !ok {
			return nil, nil, badResult{detail: fmt.Sprintf("key of row %d is not a string or a number", index+1),
				id: "BadKey", data: map[string]any{"row": index + 1}}
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		hit := interfaces.KnEventRuleHit{Key: key, Attributes: map[string]interfaces.KnEventAttribute{}}
		for column, value := range entry {
			switch {
			case column == keyColumn || value == nil:
			case column == nameColumn:
				name, ok := scalarText(value)
				if !ok {
					name = attributeText(value)
				}
				hit.Name = clip(name, objectNameMax)
			default:
				text := attributeText(value)
				hit.Attributes[column] = interfaces.KnEventAttribute{
					Key: clip(text, interfaces.KnEventKeyMax), Label: clip(text, interfaces.KnEventLabelMax),
				}
			}
		}
		hits = append(hits, hit)
	}
	if table.Limited {
		covered = make([]string, 0, len(hits))
		for _, hit := range hits {
			covered = append(covered, hit.Key)
		}
	}
	return hits, covered, nil
}
