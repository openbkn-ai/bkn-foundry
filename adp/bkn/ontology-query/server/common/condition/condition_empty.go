package condition

import (
	"context"
	"fmt"
	dtype "ontology-query/interfaces/data_type"
)

type EmptyCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
}

func NewEmptyCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	// Only string types are allowed.
	if !dtype.DataType_IsString(cfg.NameField.Type) {
		return nil, fmt.Errorf("condition [empty] left field %s is not of string type, but %s", cfg.Name, cfg.NameField.Type)
	}

	return &EmptyCond{
		mCfg:             cfg,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil

}

func (cond *EmptyCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	dslStr := `
	{
		"exists": {
			"field": "%s"
		}
	}
	`

	return fmt.Sprintf(dslStr, cond.mFilterFieldName), nil
}

// SQL has no field-existence filter, so express it as non-empty for now.
func (cond *EmptyCond) Convert2SQL(ctx context.Context) (string, error) {
	return fmt.Sprintf(`"%s" IS NOT NULL`, cond.mFilterFieldName), nil
}

func rewriteEmptyCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "empty", "field": cfg.Name})
	}

	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
