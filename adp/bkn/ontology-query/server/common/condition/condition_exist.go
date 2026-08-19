// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"
)

type ExistCond struct {
	mCfg       *CondCfg
	mfieldName string
}

func NewExistCond(cfg *CondCfg) (Condition, error) {
	return &ExistCond{
		mCfg:       cfg,
		mfieldName: cfg.Name,
	}, nil
}

func (cond *ExistCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	dslStr := `
	{
		"exists": {
			"field": "%s"
		}
	}
	`

	return fmt.Sprintf(dslStr, cond.mfieldName), nil
}

// SQL has no field-existence filter, so express it as non-empty for now.
func (cond *ExistCond) Convert2SQL(ctx context.Context) (string, error) {
	return fmt.Sprintf(`"%s" IS NOT NULL`, cond.mfieldName), nil
}

func rewriteExistCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "exist", "field": cfg.Name})
	}

	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
