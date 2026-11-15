// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	location, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func TestScheduleChecksInItsTimeZone(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		zone string
		want time.Time
	}{
		// 09:00 in Shanghai is 01:00 UTC; the default zone is Shanghai's.
		{"", time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)},
		{"Asia/Shanghai", time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC)},
		{"UTC", time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)},
		{"America/New_York", time.Date(2026, 11, 15, 14, 0, 0, 0, time.UTC)},
	} {
		check, err := parseSchedule(interfaces.KnEventCheckSchedule{Cron: "0 9 * * *", Timezone: test.zone}, now)
		if err != nil {
			t.Fatalf("%q: %v", test.zone, err)
		}
		if got := check.next(now); !got.Equal(test.want) {
			t.Fatalf("%q: next %v, want %v", test.zone, got.UTC(), test.want)
		}
	}
}

func TestScheduleRefusesWhatItCannotCheck(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	for _, input := range []interfaces.KnEventCheckSchedule{
		{Cron: ""},
		{Cron: "0 9 * *"},
		{Cron: "TZ=UTC 0 9 * * *"},
		{Cron: "CRON_TZ=UTC 0 9 * * *"},
		{Cron: "0 9 * * *", Timezone: "Mars/Olympus"},
		{Cron: "0 9 * * *", Timezone: "Local"},
		// Never fires.
		{Cron: "0 0 30 2 *"},
		// Closer than five minutes.
		{Cron: "* * * * *"},
		{Cron: "*/4 * * * *"},
		{Cron: "0,3 9 * * *"},
	} {
		if _, err := parseSchedule(input, now); !errors.As(err, new(invalidInput)) {
			t.Fatalf("%+v: %v, want refused", input, err)
		}
	}
	for _, cron := range []string{"*/5 * * * *", "0 9 * * 1-5", "0,5 9 * * *", "30 8 1 * *"} {
		if _, err := parseSchedule(interfaces.KnEventCheckSchedule{Cron: cron}, now); err != nil {
			t.Fatalf("%q: %v", cron, err)
		}
	}
}

func TestRetryBacksOffAndNeverPassesTheNextCheck(t *testing.T) {
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	hourly, err := parseSchedule(interfaces.KnEventCheckSchedule{Cron: "0 * * * *", Timezone: "UTC"}, now)
	if err != nil {
		t.Fatal(err)
	}
	for failures, wait := range map[int]time.Duration{1: time.Minute, 2: 2 * time.Minute, 3: 4 * time.Minute, 4: 8 * time.Minute, 9: 8 * time.Minute} {
		if got := retryAt(hourly, now, failures); got.Sub(now) != wait {
			t.Fatalf("failure %d: retry after %v, want %v", failures, got.Sub(now), wait)
		}
	}
	everyFive, err := parseSchedule(interfaces.KnEventCheckSchedule{Cron: "*/5 * * * *", Timezone: "UTC"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := retryAt(everyFive, now.Add(3*time.Minute), 4); !got.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("retry at %v, want the next check at %v", got, now.Add(5*time.Minute))
	}
}

func TestCheckDateIsTheDayInTheScheduleZone(t *testing.T) {
	tool := interfaces.KnEventRuleTool{BoxID: "b", ToolID: "t", Parameters: map[string]json.RawMessage{
		"today": json.RawMessage(`{"check_time":"date"}`), "days": json.RawMessage(`3`), "id": json.RawMessage(`12345678901234567890`),
	}}
	// 16:30 UTC is past midnight in Shanghai.
	at := time.Date(2026, 11, 14, 16, 30, 0, 0, time.UTC)
	for zone, want := range map[string]string{"Asia/Shanghai": "2026-11-15", "UTC": "2026-11-14"} {
		values, err := arguments(tool, at, mustZone(t, zone))
		if err != nil {
			t.Fatal(err)
		}
		if values["today"] != want || values["days"] != json.Number("3") || values["id"] != json.Number("12345678901234567890") {
			t.Fatalf("%s: arguments %v", zone, values)
		}
	}
}

func TestRuleVersionFollowsTheToolAndItsParameters(t *testing.T) {
	tool := interfaces.KnEventRuleTool{BoxID: "b", ToolID: "t", Parameters: map[string]json.RawMessage{
		"today": json.RawMessage(`{"check_time":"date"}`), "days": json.RawMessage(`3`),
	}}
	version := ruleVersion(tool, 100)
	if len(version) != ruleVersionLength || version != ruleVersion(tool, 100) {
		t.Fatalf("version %q is not stable", version)
	}
	if ruleVersion(tool, 101) == version {
		t.Fatal("a change of the tool keeps the version")
	}
	changed := tool
	changed.Parameters = map[string]json.RawMessage{"today": tool.Parameters["today"], "days": json.RawMessage(`4`)}
	if ruleVersion(changed, 100) == version {
		t.Fatal("a change of a parameter keeps the version")
	}
	other := tool
	other.ToolID = "t2"
	if ruleVersion(other, 100) == version {
		t.Fatal("another tool has the same version")
	}
}

func TestRuleToolIsStoredCompactly(t *testing.T) {
	tool := interfaces.KnEventRuleTool{BoxID: " b ", ToolID: "t", Parameters: map[string]json.RawMessage{
		"today": json.RawMessage(`{ "check_time" : "date" }`), "days": json.RawMessage(` 3 `),
	}}
	if err := normalizeRuleTool(&tool); err != nil {
		t.Fatal(err)
	}
	if tool.BoxID != "b" || string(tool.Parameters["today"]) != `{"check_time":"date"}` || string(tool.Parameters["days"]) != "3" {
		t.Fatalf("normalized %+v", tool)
	}
	same := interfaces.KnEventRuleTool{BoxID: "b", ToolID: "t", Parameters: map[string]json.RawMessage{
		"days": json.RawMessage(`3`), "today": json.RawMessage(`{"check_time":"date"}`),
	}}
	if !sameRuleTool(&tool, &same) {
		t.Fatal("the same setting compares different")
	}
	for _, parameters := range []map[string]json.RawMessage{
		{"today": json.RawMessage(`{"check_time":"time"}`)},
		{"today": json.RawMessage(`{"check_time":"date","offset":1}`)},
		{"days": json.RawMessage(`null`)},
		{"days": json.RawMessage(`{`)},
	} {
		bad := interfaces.KnEventRuleTool{BoxID: "b", ToolID: "t", Parameters: parameters}
		if err := normalizeRuleTool(&bad); !errors.As(err, new(invalidInput)) {
			t.Fatalf("%s: %v, want refused", parameters, err)
		}
	}
	if err := normalizeRuleTool(&interfaces.KnEventRuleTool{BoxID: "b"}); !errors.As(err, new(invalidInput)) {
		t.Fatalf("no tool id: %v", err)
	}
}

func TestHitsAreTheRowsOfTheResult(t *testing.T) {
	table := ruleTable{
		Columns: []string{"key", "name", "due_date", "amount", "tags"},
		Entries: []map[string]any{
			{"key": "o1", "name": "Order 1", "due_date": "2026-11-10", "amount": json.Number("12.5"), "tags": []any{"a"}},
			{"key": json.Number("12345678901234567890"), "name": nil, "due_date": nil},
			// Skipped: no key, an empty key, a key found again.
			{"key": nil, "name": "none"},
			{"key": "", "name": "empty"},
			{"key": "o1", "name": "again"},
		},
	}
	hits, covered, err := ruleHits(table)
	if err != nil {
		t.Fatal(err)
	}
	want := []interfaces.KnEventRuleHit{
		{Key: "o1", Name: "Order 1", Attributes: map[string]interfaces.KnEventAttribute{
			"due_date": {Key: "2026-11-10", Label: "2026-11-10"},
			"amount":   {Key: "12.5", Label: "12.5"},
			"tags":     {Key: `["a"]`, Label: `["a"]`},
		}},
		{Key: "12345678901234567890", Attributes: map[string]interfaces.KnEventAttribute{}},
	}
	if !reflect.DeepEqual(hits, want) || covered != nil {
		t.Fatalf("hits %+v covered %v", hits, covered)
	}
	table.Limited = true
	if _, covered, _ = ruleHits(table); !reflect.DeepEqual(covered, []string{"o1", "12345678901234567890"}) {
		t.Fatalf("a truncated result covers %v", covered)
	}
	if hits, _, err := ruleHits(ruleTable{Columns: []string{"key"}}); err != nil || hits == nil || len(hits) != 0 {
		t.Fatalf("no rows: %v %v", hits, err)
	}
}

func TestResultsACheckCannotDecideBy(t *testing.T) {
	for name, table := range map[string]ruleTable{
		"no key column":     {Columns: []string{"id"}, Entries: []map[string]any{{"id": "o1"}}},
		"key not a scalar":  {Columns: []string{"key"}, Entries: []map[string]any{{"key": map[string]any{"a": 1}}}},
		"key is a boolean":  {Columns: []string{"key"}, Entries: []map[string]any{{"key": true}}},
		"no columns at all": {},
	} {
		if _, _, err := ruleHits(table); !errors.As(err, new(badResult)) {
			t.Fatalf("%s: %v, want a bad result", name, err)
		}
	}
	// Too many attributes for an event.
	entry := map[string]any{"key": "o1"}
	columns := []string{"key"}
	for index := 0; index <= interfaces.KnEventAttributesMax; index++ {
		column := "c" + string(rune('a'+index%26)) + string(rune('a'+index/26))
		columns = append(columns, column)
		entry[column] = "v"
	}
	if _, err := checkInputOf("t", "v1", ruleTable{Columns: columns, Entries: []map[string]any{entry}}); !errors.As(err, new(badResult)) {
		t.Fatalf("too many attributes: %v", err)
	}
}

func TestParametersMatchTheToolInputs(t *testing.T) {
	definition := &interfaces.CallerTool{Inputs: []interfaces.CallerToolInput{
		{Name: "today", Type: "string", Required: true}, {Name: "days", Type: "number"}, {Name: "flag", Type: "boolean"},
	}}
	tool := func(parameters map[string]string) interfaces.KnEventRuleTool {
		raw := map[string]json.RawMessage{}
		for name, value := range parameters {
			raw[name] = json.RawMessage(value)
		}
		return interfaces.KnEventRuleTool{BoxID: "b", ToolID: "t", Parameters: raw}
	}
	if err := matchParameters(tool(map[string]string{"today": `{"check_time":"date"}`, "days": `3`}), definition); err != nil {
		t.Fatal(err)
	}
	for name, parameters := range map[string]map[string]string{
		"not an input":      {"today": `"2026-11-15"`, "limit": `3`},
		"required missing":  {"days": `3`},
		"number as string":  {"today": `"x"`, "days": `"3"`},
		"boolean as number": {"today": `"x"`, "flag": `1`},
		"date for a number": {"today": `"x"`, "days": `{"check_time":"date"}`},
	} {
		if err := matchParameters(tool(parameters), definition); !errors.As(err, new(invalidInput)) {
			t.Fatalf("%s: %v, want refused", name, err)
		}
	}
}

func TestTableOfReadsTheFunctionResult(t *testing.T) {
	table, err := tableOf(json.RawMessage(`{"columns":[{"name":"key","type":{"kind":"string"}},{"name":"n"}],
		"entries":[{"key":12345678901234567890,"n":"a"}],"limited":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(table.Columns, []string{"key", "n"}) || !table.Limited ||
		table.Entries[0]["key"] != json.Number("12345678901234567890") {
		t.Fatalf("table %+v", table)
	}
	if _, err := tableOf(json.RawMessage(`"value"`)); !errors.As(err, new(badResult)) {
		t.Fatalf("not a table: %v", err)
	}
}
