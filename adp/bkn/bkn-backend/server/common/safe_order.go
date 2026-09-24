// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for license information.

package common

import "fmt"

// SafeOrderBy builds an ORDER BY expression from known database columns and
// directions. Callers must not append unvalidated user input to the result.
func SafeOrderBy(column, direction string) (string, error) {
	var safeColumn string
	switch column {
	case "f_name":
		safeColumn = "f_name"
	case "f_create_time":
		safeColumn = "f_create_time"
	case "f_update_time":
		safeColumn = "f_update_time"
	case "f_next_run_time":
		safeColumn = "f_next_run_time"
	case "f_last_run_time":
		safeColumn = "f_last_run_time"
	default:
		return "", fmt.Errorf("unsupported order column %q", column)
	}

	var safeDirection string
	switch direction {
	case "asc", "ASC":
		safeDirection = "ASC"
	case "desc", "DESC":
		safeDirection = "DESC"
	default:
		return "", fmt.Errorf("unsupported order direction %q", direction)
	}

	return safeColumn + " " + safeDirection, nil
}
