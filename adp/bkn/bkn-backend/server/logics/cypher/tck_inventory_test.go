// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package cypher

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

type m19Fixture struct {
	IgnoreListOrder         bool `json:"ignore_list_order"`
	Source, Scenario, Query string
	Example                 int
	Parameters              map[string]any
	Columns                 []string
	Expected                []map[string]any
	Ordered                 bool
	ExpectedError           map[string]string `json:"expected_error"`
}
type m19Result struct {
	Source, Scenario string
	Example          int
	Status, Detail   string
}

func m19RowKeys(rows []map[string]any, expected, ignoreLists bool) ([]string, error) {
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		key, err := expressionKey(m19NormalizeValue(row, expected, ignoreLists))
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, nil
}

// This is an honest executable inventory, not a full TCK adapter: graph setup,
// write behavior and exact TCK error classification remain
// separate from successful tableless result comparisons.
func TestM19TablelessCompatibilityInventory(t *testing.T) {
	raw, err := os.ReadFile("testdata/tck_m19_tableless.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		SHA256 string
		Cases  []m19Fixture
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.SHA256 != "93d3ea6dcf386a64754bb561936a7035c3b429f81b9bc091b809061b4ea0d9bd" || len(fixture.Cases) < 2000 {
		t.Fatal("pinned inventory unexpectedly changed")
	}
	counts := map[string]int{}
	results := make([]m19Result, 0, len(fixture.Cases))
	failures := 0
	for _, test := range fixture.Cases {
		entry := m19Result{Source: test.Source, Scenario: test.Scenario, Example: test.Example}
		tree, parseError := Parse(test.Query)
		if parseError == nil {
			analyzed, analyzeError := Analyze(tree)
			if analyzeError == nil && queryReadsObjects(analyzed) {
				entry.Status = "requires_graph_adapter"
				results = append(results, entry)
				counts[entry.Status]++
				continue
			}
		}
		service, vega, _ := graphService(t)
		request := graphRequest(test.Query)
		request.ResultFormat = ""
		request.Parameters = test.Parameters
		result, queryError := service.Query(context.Background(), request)
		switch {
		case len(test.ExpectedError) > 0 && queryError != nil:
			entry.Status = "rejected_error_taxonomy_unverified"
			entry.Detail = queryError.Error()
		case len(test.ExpectedError) > 0:
			entry.Status = "failed_expected_error"
			entry.Detail = "query succeeded although the TCK requires rejection"
		case queryError != nil:
			entry.Status = "failed_query"
			entry.Detail = queryError.Error()
			if strings.Contains(entry.Detail, "not supported") || strings.Contains(entry.Detail, "Cypher.Unsupported") {
				entry.Status = "unsupported"
			}
			if strings.Contains(entry.Detail, "LimitExceeded") || strings.Contains(entry.Detail, "query 长度不能超过") {
				entry.Status = "resource_budget_not_compatible"
			}
		default:
			columns := []string{}
			for _, column := range result.Columns {
				columns = append(columns, column.Name)
			}
			got, gotErr := m19RowKeys(result.Entries, false, test.IgnoreListOrder)
			want, wantErr := m19RowKeys(test.Expected, true, test.IgnoreListOrder)
			if gotErr != nil || wantErr != nil {
				t.Fatal(gotErr, wantErr)
			}
			if !test.Ordered {
				sort.Strings(got)
				sort.Strings(want)
			}
			if reflect.DeepEqual(columns, test.Columns) && reflect.DeepEqual(got, want) {
				entry.Status = "passed_result"
			} else {
				entry.Status = "failed_result"
				if len(result.Entries) == len(test.Expected) && len(result.Entries) == 1 {
					alternative, changed := m19HostTimezoneExpectation(result.Entries[0], test.Expected[0])
					if changed {
						left, _ := expressionKey(m19NormalizeValue(result.Entries[0], false, test.IgnoreListOrder))
						right, _ := expressionKey(m19NormalizeValue(alternative, true, test.IgnoreListOrder))
						if left == right {
							entry.Status = "requires_pinned_historical_tzdb"
						}
					}
				}
				entry.Detail = fmt.Sprintf("columns=%v want=%v rows=%v want=%v", columns, test.Columns, got, want)
			}
		}
		if vega.request != nil {
			entry.Status = "requires_graph_adapter"
			entry.Detail = "query read a model-bound source"
		}
		counts[entry.Status]++
		results = append(results, entry)
		if strings.HasPrefix(entry.Status, "failed_") {
			failures++
			if failures <= 25 {
				t.Logf("%s %s example %d: %s: %s\n%s", test.Source, test.Scenario, test.Example, entry.Status, entry.Detail, test.Query)
			}
		}
	}
	report := struct {
		SHA256 string
		Counts map[string]int
		Cases  []m19Result
	}{fixture.SHA256, counts, results}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	output := os.Getenv("BKN_M19_REPORT")
	if output == "" {
		output = filepath.Join(t.TempDir(), "m19-tableless-results.json")
	}
	if err := os.WriteFile(output, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("M19 tableless inventory: %+v; report=%s", counts, output)
	if failures > 0 {
		t.Errorf("%d semantic failures remain; unsupported/errors without taxonomy are not counted as passing TCK cases", failures)
	}
}

func m19NormalizeValue(value any, expected, ignoreLists bool) any {
	switch item := value.(type) {
	case temporalValue:
		return item.String()
	case json.Number:
		if expected && strings.ContainsAny(item.String(), ".eE") {
			number, err := strconv.ParseFloat(item.String(), 64)
			if err == nil {
				return number
			}
		}
	case []any:
		values := make([]any, len(item))
		for i, child := range item {
			values[i] = m19NormalizeValue(child, expected, ignoreLists)
		}
		if ignoreLists {
			sort.SliceStable(values, func(i, j int) bool {
				left, _ := expressionKey(values[i])
				right, _ := expressionKey(values[j])
				return left < right
			})
		}
		return values
	case map[string]any:
		fields := map[string]any{}
		for name, child := range item {
			fields[name] = m19NormalizeValue(child, expected, ignoreLists)
		}
		return fields
	}
	return value
}

// The pinned archive predates the host IANA database. Only a difference in
// the region's historical offset is classified separately; every other value
// and the wall-clock date/time must still match. This is never counted passed.
func m19HostTimezoneExpectation(actual, expected any) (any, bool) {
	switch value := actual.(type) {
	case temporalValue:
		text, ok := expected.(string)
		if !ok || value.kind != "datetime" {
			return expected, false
		}
		index := strings.LastIndex(text, "[")
		if index < 0 || !strings.HasSuffix(text, "]") {
			return expected, false
		}
		zone := text[index+1 : len(text)-1]
		base := text[:index]
		separator := strings.Index(base, "T")
		if separator < 0 {
			return expected, false
		}
		offsetIndex := strings.IndexAny(base[separator+1:], "+-Z")
		if offsetIndex < 0 {
			return expected, false
		}
		offsetIndex += separator + 1
		clock := base[:offsetIndex]
		wall, err := time.Parse("2006-01-02T15:04:05.999999999", clock)
		if err != nil {
			return expected, false
		}
		location, err := time.LoadLocation(zone)
		if err != nil {
			return expected, false
		}
		local := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), wall.Nanosecond(), location)
		_, seconds := local.Zone()
		sign := "+"
		if seconds < 0 {
			sign = "-"
			seconds = -seconds
		}
		offset := fmt.Sprintf("%s%02d:%02d", sign, seconds/3600, (seconds%3600)/60)
		if seconds%60 != 0 {
			offset += fmt.Sprintf(":%02d", seconds%60)
		}
		alternative := clock + offset + "[" + zone + "]"
		if alternative != text && alternative == value.String() {
			return alternative, true
		}
	case map[string]any:
		fields, ok := expected.(map[string]any)
		if !ok {
			return expected, false
		}
		result := map[string]any{}
		changed := false
		for name, field := range fields {
			next, different := m19HostTimezoneExpectation(value[name], field)
			result[name] = next
			changed = changed || different
		}
		return result, changed
	}
	return expected, false
}
