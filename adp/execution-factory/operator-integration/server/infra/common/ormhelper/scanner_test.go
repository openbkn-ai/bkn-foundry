package ormhelper

import (
	"reflect"
	"testing"
)

// TestBuildFieldMap test field mapping construction.
func TestBuildFieldMap(t *testing.T) {
	type TestStruct struct {
		ID          string `db:"f_id"`
		CreateTime  int64  `db:"f_create_time"`
		Name        string `db:"f_name"`
		Description string `db:"f_description"`
	}

	structType := reflect.TypeOf(TestStruct{})
	fieldMap := buildFieldMap(structType)

	expected := map[string]int{
		"f_id":          0,
		"f_create_time": 1,
		"f_name":        2,
		"f_description": 3,
	}

	if len(fieldMap) != len(expected) {
		t.Fatalf("期望字段数量 %d，实际 %d", len(expected), len(fieldMap))
	}

	for field, expectedIndex := range expected {
		if actualIndex, exists := fieldMap[field]; !exists {
			t.Errorf("字段 %s 不存在于映射中", field)
		} else if actualIndex != expectedIndex {
			t.Errorf("字段 %s 期望索引 %d，实际 %d", field, expectedIndex, actualIndex)
		}
	}
}

// TestPrepareScanTargets test scan target preparation.
func TestPrepareScanTargets(t *testing.T) {
	type TestStruct struct {
		ID          string `db:"f_id"`
		CreateTime  int64  `db:"f_create_time"`
		Name        string `db:"f_name"`
		Description string `db:"f_description"`
	}

	// Create a structure instance.
	testStruct := TestStruct{}
	structValue := reflect.ValueOf(&testStruct).Elem()

	// Build field mapping.
	structType := reflect.TypeOf(testStruct)
	fieldMap := buildFieldMap(structType)

	// Simulate database column order (different from structure field order)
	columns := []string{"f_name", "f_description", "f_id", "f_create_time"}

	// Prepare to scan target.
	scanTargets := prepareScanTargets(structValue, columns, fieldMap)

	if len(scanTargets) != len(columns) {
		t.Fatalf("扫描目标数量 %d，期望 %d", len(scanTargets), len(columns))
	}

	// Verify that each scan target is not nil.
	for i, target := range scanTargets {
		if target == nil {
			t.Errorf("扫描目标 %d (列 %s) 为 nil", i, columns[i])
		}
	}

	t.Logf("成功创建 %d 个扫描目标", len(scanTargets))
}

// TestFieldMapping test field mapping function.
func TestFieldMapping(t *testing.T) {
	type TestStruct struct {
		ID          string `db:"f_id"`
		CreateTime  int64  `db:"f_create_time"`
		Name        string `db:"f_name"`
		Description string `db:"f_description"`
	}

	structType := reflect.TypeOf(TestStruct{})
	fieldMap := buildFieldMap(structType)

	// Test different column orders.
	testCases := []struct {
		columns  []string
		expected []int // expected field index.
	}{
		{
			columns:  []string{"f_id", "f_name", "f_description", "f_create_time"},
			expected: []int{0, 2, 3, 1},
		},
		{
			columns:  []string{"f_description", "f_create_time", "f_id", "f_name"},
			expected: []int{3, 1, 0, 2},
		},
	}

	for _, tc := range testCases {
		t.Run("columns_order", func(t *testing.T) {
			for i, column := range tc.columns {
				if fieldIndex, exists := fieldMap[column]; !exists {
					t.Errorf("列 %s 没有找到对应的字段", column)
				} else if fieldIndex != tc.expected[i] {
					t.Errorf("列 %s 期望字段索引 %d，实际 %d", column, tc.expected[i], fieldIndex)
				}
			}
		})
	}
}
