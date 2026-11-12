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

	dtype "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces/data_type"
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
				ValueFrom: ValueFrom_Const,
				Value:     "value1",
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
				ValueFrom: ValueFrom_Field,
				Value:     "value1",
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
				ValueFrom: ValueFrom_Const,
				Value:     123,
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
				ValueFrom: ValueFrom_Const,
				Value:     "value1",
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
				ValueFrom: ValueFrom_Const,
				Value:     "value1",
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
				ValueFrom: ValueFrom_Const,
				Value:     "value1",
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
				ValueFrom: ValueFrom_Const,
				Value:     "test",
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
			So(dsl, ShouldContainSubstring, `"field1": "test"`)
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
				ValueFrom: ValueFrom_Const,
				Value:     "test",
			}
			cond, _ := NewLikeCond(ctx, cfg, fieldsMap)
			likeCond := cond.(*LikeCond)

			sql, err := likeCond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldNotBeEmpty)
			So(sql, ShouldContainSubstring, "field1")
			So(sql, ShouldContainSubstring, "LIKE")
			So(sql, ShouldEqual, `"field1" LIKE 'test' ESCAPE '!'`)
		})

		Convey("escaped wildcards stay literal in the SQL pattern", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueFrom: ValueFrom_Const,
				Value:     `test'\%\_`,
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			likeCond := cond.(*LikeCond)

			sql, err := likeCond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldEqual, `"field1" LIKE 'test''!%!_' ESCAPE '!'`)
		})

		Convey("unescaped SQL wildcards keep their pattern meaning", func() {
			cfg := &CondCfg{
				Operation: OperationLike,
				Field:     "field1",
				NameField: fieldsMap["field1"],
				ValueFrom: ValueFrom_Const,
				Value:     "%test%",
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			sql, err := cond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldEqual, `"field1" LIKE '%test%' ESCAPE '!'`)
		})

		Convey("a literal SQL Server bracket remains literal", func() {
			cfg := &CondCfg{Operation: OperationLike, Field: "field1", NameField: fieldsMap["field1"],
				ValueFrom: ValueFrom_Const, Value: "a[b"}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			sql, err := cond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldEqual, `"field1" LIKE 'a![b' ESCAPE '!'`)
		})
	})
}

func TestSQLLikePatternContract(t *testing.T) {
	for _, pattern := range []string{"foo", "%foo%", "foo%", "_foo", `50\%`, `a\_b`, `a\\b`, "", "吹塑风管"} {
		if err := ValidateLikePattern(OperationLike, pattern); err != nil {
			t.Errorf("valid pattern %q: %v", pattern, err)
		}
	}
	if err := ValidateLikePattern(OperationNotLike, `foo\`); err == nil {
		t.Fatal("dangling escape was accepted")
	}
	for pattern, want := range map[string]string{
		"foo": "foo", "%foo%": "*foo*", "foo%": "foo*", "_foo": "?foo",
		`50\%`: "50%", `a\_b`: "a_b", `a\\b`: `a\\b`, `a*b?`: `a\*b\?`,
	} {
		got, err := SQLLikeToWildcard(OperationLike, pattern)
		if err != nil || got != want {
			t.Errorf("SQLLikeToWildcard(%q) = %q, %v; want %q", pattern, got, err, want)
		}
	}
	for pattern, want := range map[string]string{
		"foo": "foo", "%foo%": "%foo%", "_foo": "_foo",
		`50\%`: "50!%", `a\_b`: "a!_b", `a\\b`: `a\b`, "[a]!": "![a]!!",
	} {
		if got := SQLLikeToBangPattern(pattern); got != want {
			t.Errorf("SQLLikeToBangPattern(%q) = %q, want %q", pattern, got, want)
		}
	}
}

// Dataset filters bypass LikeCond, so validation also belongs in the converter.
func TestConvertLikeCondToDatasetFilterCondition(t *testing.T) {
	Convey("Test convertLikeCondToDatasetFilterCondition", t, func() {
		Convey("passes the raw value through so vega can parse the escapes", func() {
			got, err := convertLikeCondToDatasetFilterCondition(&CondCfg{
				Field:     "title",
				ValueFrom: ValueFrom_Const, Value: `50\%`,
			})
			So(err, ShouldBeNil)
			So(got["field"], ShouldEqual, "title")
			So(got["operation"], ShouldEqual, "like")
			So(got["value"], ShouldEqual, `50\%`)
		})

		Convey("accepts a complete pattern without adding wildcards", func() {
			got, err := convertLikeCondToDatasetFilterCondition(&CondCfg{
				Field:     "title",
				ValueFrom: ValueFrom_Const, Value: "%Indirect%",
			})
			So(err, ShouldBeNil)
			So(got["value"], ShouldEqual, "%Indirect%")
		})

		Convey("rejects a dangling escape and names the property", func() {
			_, err := convertLikeCondToDatasetFilterCondition(&CondCfg{
				Field: "title", ValueFrom: ValueFrom_Const, Value: `foo\`,
			})
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, "property 'title'")
			So(err.Error(), ShouldContainSubstring, "incomplete escape")
		})
	})
}
