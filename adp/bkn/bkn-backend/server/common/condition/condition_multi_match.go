// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"fmt"

	"github.com/bytedance/sonic"

	"bkn-backend/common"
)

type MultiMatchCond struct {
	mCfg              *CondCfg
	mFilterFieldNames []string
}

func NewMultiMatchCond(ctx context.Context, cfg *CondCfg, fieldScope uint8, fieldsMap map[string]*ViewField) (Condition, error) {

	// Read fields from cfg.RemainCfg. This is the multi_match fields array.
	// To match all fields, omit this value or pass ["*"]. A plain string * is not supported.
	var fields []string
	cfgFields, exist := cfg.RemainCfg["fields"]
	if exist {
		// fields must be an array when present.
		if !common.IsSlice(cfgFields) {
			return nil, fmt.Errorf("condition [multi_match] 'fields' value should be an array")
		}
		// The fields array must contain strings.
		for _, cfgField := range cfgFields.([]any) {
			field, ok := cfgField.(string)
			if !ok {
				return nil, fmt.Errorf("condition [multi_match] 'fields' value should be a string array, contain non string value[%v]", cfgField)
			}

			// Each fields array element must be a string.
			name := getFilterFieldName(field, fieldsMap, true)
			if name == AllField {
				fields = []string{
					"id",
					"name",
					"comment",
					"detail",
					"data_properties.name",
					"data_properties.display_name",
					"data_properties.comment",
					"logic_properties.name",
					"logic_properties.display_name",
					"logic_properties.comment",
				}
				// Stop the loop when * is encountered.
				break
			} else {
				fields = append(fields, name)
			}
		}
	}

	// Validate match_type. It may be empty.
	matchType, exist := cfg.RemainCfg["match_type"]
	if exist && matchType != "" {
		mtype, ok := matchType.(string)
		if !ok {
			return nil, fmt.Errorf("condition [multi_match] 'match_type' value should be a string, actual is[%v]", matchType)
		}
		if !MatchTypeMap[mtype] {
			return nil, fmt.Errorf("condition [multi_match] 'match_type' value should be one of [%v], actual is[%v]", MatchTypeMap, mtype)
		}
	}

	return &MultiMatchCond{
		mCfg:              cfg,
		mFilterFieldNames: fields,
	}, nil
}

func (cond *MultiMatchCond) Convert(ctx context.Context, vectorizer func(ctx context.Context, words []string) ([]*VectorResp, error)) (string, error) {
	v := cond.mCfg.Value
	vStr, ok := v.(string)
	if ok {
		v = fmt.Sprintf("%q", vStr)
	}

	fields, err := sonic.Marshal(cond.mFilterFieldNames)
	if err != nil {
		return "", fmt.Errorf("condition [multi_match] marshal fields error: %s", err.Error())
	}

	// Defaults to best_fields.
	matchType := "best_fields"
	if mt, exist := cond.mCfg.RemainCfg["match_type"]; exist {
		if mtStr, ok := mt.(string); exist && ok {
			matchType = mtStr
		} else {
			return "", fmt.Errorf("condition [multi_match] match_type[%v] should be a string", mt)
		}
	}

	dslStr := fmt.Sprintf(`
					{
						"multi_match": {
							"query": %v,
							"type": "%s"`, v, matchType)

	// When fields is omitted, query index.query.default_field, which defaults to *.
	if len(cond.mFilterFieldNames) > 0 {
		dslStr = fmt.Sprintf(`%s,
							"fields": %v
						}
					}`, dslStr, string(fields))
	} else {
		dslStr = fmt.Sprintf(`%s
						}
					}`, dslStr)
	}

	return dslStr, nil
}

func (cond *MultiMatchCond) Convert2SQL(ctx context.Context) (string, error) {
	return "", nil
}

// convertMultiMatchCondToDatasetFilterCondition converts MultiMatchCond to dataset filter condition format
func convertMultiMatchCondToDatasetFilterCondition(cfg *CondCfg, fieldsMap map[string]*ViewField) (map[string]any, error) {
	// Get the fields list.
	var fields []string
	cfgFields, exist := cfg.RemainCfg["fields"]
	if exist {
		// fields must be an array when present.
		if !common.IsSlice(cfgFields) {
			return nil, fmt.Errorf("condition [multi_match] 'fields' value should be an array")
		}
		// The fields array must contain strings.
		for _, cfgField := range cfgFields.([]any) {
			field, ok := cfgField.(string)
			if !ok {
				return nil, fmt.Errorf("condition [multi_match] 'fields' value should be a string array, contain non string value[%v]", cfgField)
			}

			// Each fields array element must be a string.
			name := getFilterFieldName(field, fieldsMap, true)
			if name == AllField {
				fields = []string{
					"id",
					"name",
					"comment",
					"detail",
					"data_properties.name",
					"data_properties.display_name",
					"data_properties.comment",
					"logic_properties.name",
					"logic_properties.display_name",
					"logic_properties.comment",
				}
				// Stop the loop when * is encountered.
				break
			} else {
				fields = append(fields, name)
			}
		}
	}

	return map[string]any{
		"operation":  "multi_match",
		"fields":     fields, // Field list
		"value":      cfg.Value,
		"value_from": "const",
	}, nil
}
