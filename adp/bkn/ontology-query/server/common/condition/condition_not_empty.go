package condition

import (
	"context"
	"fmt"
	dtype "ontology-query/interfaces/data_type"
)

type NotEmptyCond struct {
	mCfg             *CondCfg
	mFilterFieldName string
}

func NewNotEmptyCond(ctx context.Context, cfg *CondCfg, fieldsMap map[string]*DataProperty) (Condition, error) {
	// Only string types are allowed.
	if !dtype.DataType_IsString(cfg.NameField.Type) {
		return nil, fmt.Errorf("condition [empty] left field %s is not of string type, but %s", cfg.Name, cfg.NameField.Type)
	}

	return &NotEmptyCond{
		mCfg:             cfg,
		mFilterFieldName: getFilterFieldName(cfg.Name, fieldsMap, false),
	}, nil

}

func (cond *NotEmptyCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error)) (string, error) {
	dslStr := fmt.Sprintf(`
	{
		"bool": {
			"must": {
				"exists": {
					"field": "%s"
				}
			},
			"must_not": {
				"term": {
					"%s": ""
				}
			}
		}
	}`, cond.mFilterFieldName, cond.mFilterFieldName)

	return dslStr, nil
}

// SQL has no field-existence filter, so express it as non-empty for now.
func (cond *NotEmptyCond) Convert2SQL(ctx context.Context) (string, error) {
	sqlStr := fmt.Sprintf(`"%s" IS NOT NULL AND "%s" <> ''`, cond.mFilterFieldName, cond.mFilterFieldName)
	return sqlStr, nil
}

func rewriteNotEmptyCond(ctx context.Context, cfg *CondCfg) (*CondCfg, error) {

	// Replace property fields in filter conditions with mapped view fields.
	if cfg.NameField.Name == "" {
		return nil, validationError(ctx, "OperatorFieldNotFound", map[string]any{"operation": "not_empty", "field": cfg.Name})
	}

	return &CondCfg{
		Name:        cfg.NameField.MappedField.Name,
		Operation:   cfg.Operation,
		ValueOptCfg: cfg.ValueOptCfg,
	}, nil
}
