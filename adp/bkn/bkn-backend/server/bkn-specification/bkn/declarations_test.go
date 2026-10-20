// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package bkn

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadV3_ReadsPropertyDeclarations(t *testing.T) {
	net, err := loadV3(t)
	require.NoError(t, err)
	props := map[string]*DataProperty{}
	for _, p := range net.ObjectTypes[0].DataProperties {
		props[p.Name] = p
	}
	assert.True(t, props["id"].Required, "a primary key is required by definition")
	assert.True(t, props["pod_name"].Required)
	assert.False(t, props["cpu_pct"].Required)
	assert.Equal(t, SemanticEventTime, props["created_at"].Semantic)
	assert.Equal(t, &PropertyConstraints{Range: &ValueRange{Min: int64(0), Max: int64(100)}, Unit: "%"}, props["cpu_pct"].Constraints)
	assert.Equal(t, &PropertyConstraints{Timezone: "Asia/Shanghai"}, props["created_at"].Constraints)
	assert.Nil(t, props["id"].Constraints)
}

// Under 2.x the new columns are unknown and ignored, and the section goes into the description,
// exactly as before 3.0 existed.
func TestLoadLegacy_IgnoresPropertyDeclarations(t *testing.T) {
	net, err := loadV3(t, v3Edit{fNet, "bkn_spec: \"3.0\"\n", ""})
	require.NoError(t, err)
	ot := net.ObjectTypes[0]
	for _, p := range ot.DataProperties {
		assert.Empty(t, p.Semantic)
		assert.False(t, p.Required)
		assert.Nil(t, p.Constraints)
	}
	assert.Contains(t, ot.Description, "### Property Constraints")
}

const (
	dpRow      = "| cpu_pct | CPU | float | | | | cpu_pct |"
	dpPodName  = "| pod_name | 名称 | string | | true | | pod_name |"
	dpCreated  = "| created_at | 创建时间 | datetime | event_time | | | created_at |"
	pcBlock    = "cpu_pct:\n  range: {min: 0, max: 100}\n  unit: \"%\"\ncreated_at:\n  timezone: Asia/Shanghai\n"
	pcCPURange = "  range: {min: 0, max: 100}\n"
)

func TestLoadV3_RefusesWrongDeclarations(t *testing.T) {
	cases := []struct {
		name  string
		edits []v3Edit
		want  string
	}{
		{"unknown Semantic", []v3Edit{{fObj, dpRow, "| cpu_pct | CPU | float | metric | | | cpu_pct |"}}, `Semantic "metric" is unknown`},
		{"Semantic and Type", []v3Edit{{fObj, dpRow, "| cpu_pct | CPU | float | event_time | | | cpu_pct |"}}, `Semantic event_time needs Type date, datetime, timestamp, got "float"`},
		{"Semantic twice", []v3Edit{{fObj, dpPodName, "| pod_name | 名称 | string | status | true | | pod_name |"}, {fObj, "| id | ID | string | | | | id |", "| id | ID | string | status | | | id |"}}, "both declare Semantic status"},
		{"end_time without event_time", []v3Edit{{fObj, dpCreated, "| created_at | 创建时间 | datetime | end_time | | | created_at |"}}, "Semantic end_time needs an event_time property"},
		{"latitude without longitude", []v3Edit{{fObj, dpRow, "| cpu_pct | CPU | float | latitude | | | cpu_pct |"}}, "latitude and longitude are declared in pairs"},
		{"location with latitude", []v3Edit{
			{fObj, dpRow, "| cpu_pct | CPU | float | latitude | | | cpu_pct |\n| lon | 经度 | float | longitude | | | lon |\n| geo | 位置 | point | location | | | geo |"},
			{fObj, pcCPURange, ""},
			{fObj, "  unit: \"%\"\n", "  crs: wgs84\n"},
		}, "Semantic location and latitude / longitude describe the same position"},
		{"Required value", []v3Edit{{fObj, dpPodName, "| pod_name | 名称 | string | | yes | | pod_name |"}}, `Required must be true, false or empty, got "yes"`},
		{"Required false on a primary key", []v3Edit{{fObj, "| id | ID | string | | | | id |", "| id | ID | string | | false | | id |"}}, `data property "id": a primary key is required by definition`},
		{"constraint on an unknown property", []v3Edit{{fObj, pcBlock, "memory:\n  unit: MB\n"}}, `property constraints: "memory" is not a data property`},
		{"unknown constraint key", []v3Edit{{fObj, "  unit: \"%\"\n", "  scale: 2\n"}}, `property constraints "cpu_pct" scale is not a constraint`},
		{"range on a string", []v3Edit{{fObj, pcBlock, "pod_name:\n  range: {min: 1}\n"}}, "range needs a numeric, date, datetime or timestamp Type"},
		{"range min above max", []v3Edit{{fObj, pcCPURange, "  range: {min: 100, max: 0}\n"}}, "range min 100 is greater than max 0"},
		{"range of a numeric property with text", []v3Edit{{fObj, pcCPURange, "  range: {min: low}\n"}}, "range of a numeric property takes numbers"},
		{"range of a datetime property", []v3Edit{{fObj, "created_at:\n  timezone: Asia/Shanghai\n", "created_at:\n  range: {min: \"2020-01-01\"}\n"}}, `range of a datetime property takes ISO 8601 times such as "2020-01-01T00:00:00Z"`},
		{"datetime range order", []v3Edit{{fObj, "created_at:\n  timezone: Asia/Shanghai\n", "created_at:\n  range: {min: \"2021-01-01T00:00:00Z\", max: \"2020-01-01T00:00:00+08:00\"}\n"}}, "is later than max"},
		{"length on a number", []v3Edit{{fObj, pcCPURange, "  length: {max: 3}\n"}}, "length needs Type string, keyword, text"},
		{"length min above max", []v3Edit{{fObj, pcBlock, "pod_name:\n  length: {min: 9, max: 3}\n"}}, "length min 9 is greater than max 3"},
		{"negative length", []v3Edit{{fObj, pcBlock, "pod_name:\n  length: {min: -1}\n"}}, "length min must be a non-negative integer"},
		{"pattern that does not compile", []v3Edit{{fObj, pcBlock, "pod_name:\n  pattern: \"(?=x)\"\n"}}, "pattern is not a valid RE2 expression"},
		{"pattern on a number", []v3Edit{{fObj, pcCPURange, "  pattern: \"^1$\"\n"}}, "pattern needs Type string, keyword, text"},
		{"unit on a string", []v3Edit{{fObj, pcBlock, "pod_name:\n  unit: 个\n"}}, `unit needs a numeric Type, got "string"`},
		{"unique on a point", []v3Edit{{fObj, dpRow, dpRow + "\n| geo | 位置 | point | location | | | geo |"}, {fObj, pcBlock, pcBlock + "geo:\n  unique: true\n"}}, `unique does not apply to Type "point"`},
		{"unique that is not a bool", []v3Edit{{fObj, pcBlock, "pod_name:\n  unique: \"yes\"\n"}}, "unique must be true or false"},
		{"crs without a position Semantic", []v3Edit{{fObj, "  unit: \"%\"\n", "  crs: wgs84\n"}}, "crs applies to a property whose Semantic is location, latitude or longitude"},
		{"crs value", []v3Edit{{fObj, dpRow, dpRow + "\n| geo | 位置 | point | location | | | geo |"}, {fObj, pcBlock, pcBlock + "geo:\n  crs: utm\n"}}, `crs must be wgs84, gcj02, bd09, got "utm"`},
		{"latitude and longitude crs differ", []v3Edit{
			{fObj, dpRow, "| cpu_pct | CPU | float | latitude | | | cpu_pct |\n| lon | 经度 | float | longitude | | | lon |"},
			{fObj, "  unit: \"%\"\n", "  crs: wgs84\nlon:\n  crs: gcj02\n"},
		}, "latitude and longitude must use the same crs, got wgs84 and gcj02"},
		{"timezone on a timestamp", []v3Edit{{fObj, dpCreated, "| created_at | 创建时间 | timestamp | event_time | | | created_at |"}}, `timezone applies to Type datetime, got "timestamp"`},
		{"timezone name", []v3Edit{{fObj, "timezone: Asia/Shanghai", "timezone: Beijing"}}, `timezone "Beijing" is not an IANA time zone name`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadV3(t, c.edits...)
			require.Error(t, err)
			assert.Contains(t, err.Error(), c.want)
			var specErr *SpecError
			assert.ErrorAs(t, err, &specErr)
		})
	}
}

// The checks import runs are the ones a save through the API runs.
func TestValidateObjectTypeDeclarations(t *testing.T) {
	ot := &BknObjectType{DataProperties: []*DataProperty{
		{Name: "at", Type: "date", Semantic: SemanticValidFrom, Constraints: &PropertyConstraints{Range: &ValueRange{Min: "2020-01-01", Max: "2020-12-31"}}},
		{Name: "code", Type: "keyword", Constraints: &PropertyConstraints{Length: &LengthRange{Max: intPtr(8)}, Pattern: `^[A-Z]{2}\d{6}$`, Unique: true}},
	}}
	require.NoError(t, ValidateObjectTypeDeclarations(ot))

	ot.DataProperties[0].Constraints.Range.Max = "2019-12-31"
	assert.ErrorContains(t, ValidateObjectTypeDeclarations(ot), "is later than max")
	ot.DataProperties[0].Constraints.Range.Max = "2020/12/31"
	assert.ErrorContains(t, ValidateObjectTypeDeclarations(ot), `range of a date property takes ISO 8601 dates such as "2020-01-01"`)
}

func intPtr(n int) *int { return &n }
