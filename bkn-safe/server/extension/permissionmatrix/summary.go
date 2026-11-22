// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package permissionmatrix exposes configuration counts only. It never
// recomputes effective row predicates or field visibility in Core.
package permissionmatrix

import (
	"context"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/permdata"
	"github.com/openbkn-ai/bkn-foundry/bkn-safe/server/extension/rowfilter"
	"github.com/openbkn-ai/bkn-foundry/comm-go/entitlement"
	"sync"
)

type Request struct {
	SubjectType    string
	SubjectID      string
	RoleIDs        []string
	ObjectTypeRefs []string
}
type Summary struct {
	RowPolicies   int `json:"row_policies"`
	RowConditions int `json:"row_conditions"`
	FieldRules    int `json:"field_rules"`
	MaskedRules   int `json:"masked_rules"`
	HiddenRules   int `json:"hidden_rules"`
}
type Provider interface {
	Summarize(context.Context, Request) (map[string]Summary, error)
}

var mu sync.RWMutex
var provider Provider

func Register(value Provider) {
	entitlement.MustBeAssembling("permissionmatrix")
	mu.Lock()
	defer mu.Unlock()
	if value == nil || provider != nil {
		panic("permissionmatrix: invalid provider registration")
	}
	provider = value
}
func Available() bool {
	mu.RLock()
	defer mu.RUnlock()
	return provider != nil && permdata.Available() && rowfilter.Available()
}
func Summarize(ctx context.Context, request Request) (map[string]Summary, error) {
	if !Available() {
		return nil, nil
	}
	mu.RLock()
	current := provider
	mu.RUnlock()
	return current.Summarize(ctx, request)
}
