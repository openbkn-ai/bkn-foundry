// Copyright openbkn.ai
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"testing"
)

func TestConvertNotExistCondToDatasetFilterCondition(t *testing.T) {
	got, err := ConvertCondCfgToFilterCondition(context.Background(),
		&CondCfg{Field: "lifecycle_status", Operation: OperationNotExist}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got["field"] != "lifecycle_status" || got["operation"] != "not_exist" {
		t.Fatalf("got %v", got)
	}
	if _, err := ConvertCondCfgToFilterCondition(context.Background(),
		&CondCfg{Operation: OperationNotExist}, nil, nil); err == nil {
		t.Fatal("empty field accepted")
	}
}
