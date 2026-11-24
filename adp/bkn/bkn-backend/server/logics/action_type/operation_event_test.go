// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package action_type

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
)

func recognitionOf(id, class string) *interfaces.ActionType {
	return &interfaces.ActionType{ActionTypeWithKeyField: interfaces.ActionTypeWithKeyField{
		ATID:            id,
		OperationConfig: &interfaces.ActionOperationConfig{Type: interfaces.ActionOperationEvent, EventClass: class},
	}}
}

func TestOneRecognitionActionOfAClassPerSave(t *testing.T) {
	if err := checkOneRecognitionEach(context.Background(), []*interfaces.ActionType{
		recognitionOf("a", "late_order"), recognitionOf("b", "low_stock"), {},
	}); err != nil {
		t.Fatal(err)
	}
	// IDs are given after the check: two new ones without an ID are two as well.
	err := checkOneRecognitionEach(context.Background(), []*interfaces.ActionType{
		recognitionOf("", "late_order"), recognitionOf("", "late_order"),
	})
	var httpErr *httperrors.HTTPError
	if !errors.As(err, &httpErr) || httpErr.HTTPCode != http.StatusConflict {
		t.Fatalf("%v, want 409", err)
	}
}
