package ormhelper

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
)

// buildFieldMap builds a field mapping table.
func buildFieldMap(structType reflect.Type) map[string]int {
	fieldMap := make(map[string]int)
	numField := structType.NumField()
	for i := 0; i < numField; i++ {
		field := structType.Field(i)
		tag := field.Tag.Get("db")
		if tag == "" {
			tag = field.Tag.Get("json")
		}
		if tag != "" && tag != "-" {
			// Process the options in the tag and only take the field name.
			if idx := strings.Index(tag, ","); idx != -1 {
				tag = tag[:idx]
			}
			fieldMap[tag] = i
		}
	}
	return fieldMap
}

// prepareScanTargets prepares scanning targets.
func prepareScanTargets(structValue reflect.Value, columns []string, fieldMap map[string]int) []interface{} {
	scanTargets := make([]interface{}, len(columns))
	for i, column := range columns {
		if fieldIndex, exists := fieldMap[column]; exists {
			fieldValue := structValue.Field(fieldIndex)
			if fieldValue.CanSet() {
				scanTargets[i] = fieldValue.Addr().Interface()
			} else {
				var dummy interface{}
				scanTargets[i] = &dummy
			}
		} else {
			var dummy interface{}
			scanTargets[i] = &dummy
		}
	}
	return scanTargets
}

// structScanner structure scanner.
type structScanner struct{}

// NewScanner creates a new scanner.
func NewScanner() Scanner {
	return &structScanner{}
}

// ScanOne scans a single line into a structure.
// Since sql.Row does not have a Columns() method, column information cannot be obtained for field mapping.
// This method is now deprecated, it is recommended to use ScanOneWithColumns.
func (s *structScanner) ScanOne(row *sql.Row, dest interface{}) error {
	return fmt.Errorf("ScanOne method is deprecated due to lack of column information in sql.Row. Use ScanOneWithColumns instead")
}

// ScanOneWithColumns scans a single row into a structure (supports field mapping)
func (s *structScanner) ScanOneWithColumns(row *sql.Row, dest interface{}, columns []string) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Ptr {
		return fmt.Errorf("dest must be a pointer")
	}

	destValue = destValue.Elem()
	if destValue.Kind() != reflect.Struct {
		return fmt.Errorf("dest must be a pointer to struct")
	}

	destType := destValue.Type()

	// Create field mapping.
	fieldMap := buildFieldMap(destType)

	// Prepare to scan target.
	scanTargets := prepareScanTargets(destValue, columns, fieldMap)

	return row.Scan(scanTargets...)
}

// ScanMany scans multiple rows into structure slices.
func (s *structScanner) ScanMany(rows *sql.Rows, dest interface{}) error {
	destValue := reflect.ValueOf(dest)
	if destValue.Kind() != reflect.Ptr {
		return fmt.Errorf("dest must be a pointer")
	}

	destValue = destValue.Elem()
	if destValue.Kind() != reflect.Slice {
		return fmt.Errorf("dest must be a pointer to slice")
	}

	// Get slice element type.
	sliceType := destValue.Type()
	elemType := sliceType.Elem()

	// If it is a pointer type, get the actual structure type.
	structType := elemType
	isPointer := false
	if elemType.Kind() == reflect.Ptr {
		isPointer = true
		structType = elemType.Elem()
	}

	if structType.Kind() != reflect.Struct {
		return fmt.Errorf("slice element must be struct or pointer to struct")
	}

	// Get column information.
	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	// Create field mapping.
	fieldMap := buildFieldMap(structType)

	// Scan all lines.
	results := reflect.MakeSlice(sliceType, 0, 0)
	for rows.Next() {
		// Create a new structure instance.
		var elemValue reflect.Value
		if isPointer {
			elemValue = reflect.New(structType)
		} else {
			elemValue = reflect.New(structType).Elem()
		}

		structValue := elemValue
		if isPointer {
			structValue = elemValue.Elem()
		}

		// Prepare to scan target.
		scanTargets := prepareScanTargets(structValue, columns, fieldMap)

		if err := rows.Scan(scanTargets...); err != nil {
			return err
		}

		results = reflect.Append(results, elemValue)
	}

	destValue.Set(results)
	return rows.Err()
}
