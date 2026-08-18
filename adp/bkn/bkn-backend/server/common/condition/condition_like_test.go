// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	dtype "bkn-backend/interfaces/data_type"
)

func TestNewLikeCond(t *testing.T) {
	Convey("Test NewLikeCond", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*ViewField{
			"field1": {
				Name: "field1",
				Type: dtype.DATATYPE_STRING,
			},
			"field2": {
				Name: "field2",
				Type: dtype.DATATYPE_TEXT,
			},
			"field3": {
				Name: "field3",
				Type: dtype.DATATYPE_INTEGER,
			},
			"field4": {
				Name: "field4",
				Type: dtype.CHAR,
			},
		}

		Convey("non-string field should return error", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field3",
				NameField: fieldsMap["field3"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     "value1",
				},
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
			So(err.Error(), ShouldContainSubstring, "not a string field")
		})

		Convey("invalid value_from should return error", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Field,
					Value:     "value1",
				},
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
			So(err.Error(), ShouldContainSubstring, "does not support value_from type")
		})

		Convey("non-string value should return error", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     123,
				},
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
			So(err.Error(), ShouldContainSubstring, "not a string value")
		})

		Convey("valid string field and value should create LikeCond", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     "value1",
				},
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
			likeCond, ok := cond.(*LikeCond)
			So(ok, ShouldBeTrue)
			So(likeCond.mValue, ShouldEqual, "value1")
		})

		Convey("text field should work", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field2",
				NameField: fieldsMap["field2"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     "value1",
				},
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})

		Convey("char field should work", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field4",
				NameField: fieldsMap["field4"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     "value1",
				},
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})
	})
}

func TestLikeCond_Convert(t *testing.T) {
	Convey("Test LikeCond.Convert", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*ViewField{
			"field1": {
				Name: "field1",
				Type: dtype.DATATYPE_STRING,
			},
		}

		Convey("should create regexp query", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     "test",
				},
			}
			cond, _ := NewLikeCond(ctx, cfg, fieldsMap)
			likeCond := cond.(*LikeCond)

			vectorizer := func(ctx context.Context, words []string) ([]*VectorResp, error) {
				return nil, nil
			}

			dsl, err := likeCond.Convert(ctx, vectorizer)
			So(err, ShouldBeNil)
			So(dsl, ShouldNotBeEmpty)
			So(dsl, ShouldContainSubstring, "wildcard")
			So(dsl, ShouldContainSubstring, "field1")
			So(dsl, ShouldContainSubstring, "*test*")
		})
	})
}

func TestLikeCond_Convert2SQL(t *testing.T) {
	Convey("Test LikeCond.Convert2SQL", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*ViewField{
			"field1": {
				Name: "field1",
				Type: dtype.DATATYPE_STRING,
			},
		}

		Convey("should create LIKE query", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     "test",
				},
			}
			cond, _ := NewLikeCond(ctx, cfg, fieldsMap)
			likeCond := cond.(*LikeCond)

			sql, err := likeCond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldNotBeEmpty)
			So(sql, ShouldContainSubstring, "field1")
			So(sql, ShouldContainSubstring, "LIKE")
			So(sql, ShouldContainSubstring, "test")
		})

		Convey("escaped wildcards stay literal in the SQL pattern", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     `test'\%\_`,
				},
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			likeCond := cond.(*LikeCond)

			sql, err := likeCond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			// The outer % pair comes from the contains semantics; the % and _ inside the value
			// are escaped into literals
			So(sql, ShouldEqual, `"field1" LIKE '%test\'\%\_%'`)
		})

		Convey("unescaped SQL wildcards are rejected", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueOptCfg: ValueOptCfg{
					ValueFrom: ValueFrom_Const,
					Value:     "%test%",
				},
			}
			_, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "literal substring")
		})
	})
}

func TestParseLikeValue(t *testing.T) {
	Convey("Test ParseLikeValue", t, func() {
		Convey("plain substring passes through", func() {
			literal, err := ParseLikeValue(OperationLike, "Indirect")
			So(err, ShouldBeNil)
			So(literal, ShouldEqual, "Indirect")
		})

		Convey("cjk substring passes through", func() {
			literal, err := ParseLikeValue(OperationLike, "吹塑风管")
			So(err, ShouldBeNil)
			So(literal, ShouldEqual, "吹塑风管")
		})

		// _ was literal on every path before this change, so rejecting it is collateral damage:
		// search terms containing an underscore are common
		Convey("bare underscore stays a literal", func() {
			literal, err := ParseLikeValue(OperationLike, "object_type")
			So(err, ShouldBeNil)
			So(literal, ShouldEqual, "object_type")
		})

		Convey("escaped wildcards become literals", func() {
			literal, err := ParseLikeValue(OperationLike, `50\%`)
			So(err, ShouldBeNil)
			So(literal, ShouldEqual, "50%")

			literal, err = ParseLikeValue(OperationLike, `a\_b`)
			So(err, ShouldBeNil)
			So(literal, ShouldEqual, "a_b")
		})

		Convey("unescaped wildcards are rejected with a usable message", func() {
			_, err := ParseLikeValue(OperationLike, "%Indirect%")
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "literal substring")
			So(err.Error(), ShouldContainSubstring, "[regex]")

			_, err = ParseLikeValue(OperationNotLike, "b%")
			So(err, ShouldNotBeNil)
		})
	})
}

// query_object_instance takes the dataset filter path, which never builds a LikeCond, so the
// contract check has to live in the converter too — otherwise "%foo%" travels all the way to
// vega before anything stops it.
func TestConvertLikeCondToDatasetFilterCondition(t *testing.T) {
	Convey("Test convertLikeCondToDatasetFilterCondition", t, func() {
		Convey("passes the raw value through so vega can parse the escapes", func() {
			got, err := convertLikeCondToDatasetFilterCondition(&CondCfg{
				Field:       "title",
				ValueOptCfg: ValueOptCfg{ValueFrom: ValueFrom_Const, Value: `50\%`},
			})
			So(err, ShouldBeNil)
			So(got["field"], ShouldEqual, "title")
			So(got["operation"], ShouldEqual, "like")
			So(got["value"], ShouldEqual, `50\%`)
		})

		Convey("rejects SQL wildcards and names the property", func() {
			_, err := convertLikeCondToDatasetFilterCondition(&CondCfg{
				Field:       "title",
				ValueOptCfg: ValueOptCfg{ValueFrom: ValueFrom_Const, Value: "%Indirect%"},
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "property 'title'")
			So(err.Error(), ShouldContainSubstring, "literal substring")
		})
	})
}
