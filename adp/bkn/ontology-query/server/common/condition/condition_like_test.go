// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"strings"
	"testing"

	dtype "github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces/data_type"

	. "github.com/smartystreets/goconvey/convey"
)

func Test_NewLikeCond(t *testing.T) {
	Convey("Test NewLikeCond", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*DataProperty{
			"name": {
				Name: "name",
				Type: dtype.DATATYPE_STRING,
				MappedField: Field{
					Name: "mapped_name",
				},
			},
			"age": {
				Name: "age",
				Type: dtype.DATATYPE_INTEGER,
				MappedField: Field{
					Name: "mapped_age",
				},
			},
		}

		Convey("成功 - 字符串字段", func() {
			cfg := &CondCfg{
				Name:      "name",
				Operation: OperationLike,
				Value:     "test%",
			}
			cfg.NameField = fieldsMap["name"]
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})

		Convey("失败 - 字段不存在", func() {
			cfg := &CondCfg{
				Name:      "nonexistent",
				Operation: OperationLike,
				Value:     "test%",
			}
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})

		Convey("失败 - 非字符串字段", func() {
			cfg := &CondCfg{
				Name:      "age",
				Operation: OperationLike,
				Value:     "test%",
			}
			cfg.NameField = fieldsMap["age"]
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})

		Convey("失败 - 非字符串值", func() {
			cfg := &CondCfg{
				Name:      "name",
				Operation: OperationLike,
				Value:     123,
			}
			cfg.NameField = fieldsMap["name"]
			cond, err := NewLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})
	})
}

func Test_LikeCond_Convert(t *testing.T) {
	Convey("Test LikeCond Convert", t, func() {
		ctx := context.Background()

		Convey("成功 - 转换DSL", func() {
			cond := &LikeCond{
				mCfg: &CondCfg{
					Value: "test",
				},
				mValue:           "test",
				mFilterFieldName: "name",
			}
			result, err := cond.Convert(ctx, nil)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"wildcard"`)
			So(result, ShouldContainSubstring, `"name": "test"`)
			So(result, ShouldContainSubstring, `"name"`)
		})
	})
}

func Test_LikeCond_Convert2SQL(t *testing.T) {
	Convey("Test LikeCond Convert2SQL", t, func() {
		ctx := context.Background()

		Convey("成功 - 转换SQL", func() {
			cond := &LikeCond{
				mCfg: &CondCfg{
					Value: "test",
				},
				mValue:           "test",
				mFilterFieldName: "name",
			}
			result, err := cond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"name"`)
			So(result, ShouldEqual, `"name" LIKE 'test' ESCAPE '!'`)
		})
	})
}

func Test_rewriteLikeCond(t *testing.T) {
	Convey("Test rewriteLikeCond", t, func() {
		Convey("成功 - 重写条件", func() {
			cfg := &CondCfg{
				Name:      "name",
				Operation: OperationLike,
				Value:     "test%",
				NameField: &DataProperty{
					Name: "name",
					MappedField: Field{
						Name: "mapped_name",
					},
				},
			}
			result, err := rewriteLikeCond(context.Background(), cfg)
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(result.Name, ShouldEqual, "mapped_name")
		})

		Convey("失败 - NameField为空", func() {
			cfg := &CondCfg{
				Name:      "name",
				Operation: OperationLike,
				Value:     "test%",
				NameField: &DataProperty{
					Name: "",
				},
			}
			result, err := rewriteLikeCond(context.Background(), cfg)
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})
	})
}

func Test_NewNotLikeCond(t *testing.T) {
	Convey("Test NewNotLikeCond", t, func() {
		ctx := context.Background()
		fieldsMap := map[string]*DataProperty{
			"name": {
				Name: "name",
				Type: dtype.DATATYPE_STRING,
				MappedField: Field{
					Name: "mapped_name",
				},
			},
		}

		Convey("成功 - 创建NOT LIKE条件", func() {
			cfg := &CondCfg{
				Name:      "name",
				Operation: OperationNotLike,
				Value:     "test%",
			}
			cfg.NameField = fieldsMap["name"]
			cond, err := NewNotLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})

		Convey("失败 - 非字符串值", func() {
			cfg := &CondCfg{
				Name:      "name",
				Operation: OperationNotLike,
				Value:     123,
			}
			cfg.NameField = fieldsMap["name"]
			cond, err := NewNotLikeCond(ctx, cfg, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})
	})
}

func Test_NotLikeCond_Convert(t *testing.T) {
	Convey("Test NotLikeCond Convert", t, func() {
		ctx := context.Background()

		Convey("成功 - 转换DSL", func() {
			cond := &NotLikeCond{
				mCfg: &CondCfg{
					Value: "test",
				},
				mValue:           "test",
				mFilterFieldName: "name",
			}
			result, err := cond.Convert(ctx, nil)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"bool"`)
			So(result, ShouldContainSubstring, `"must_not"`)
			So(result, ShouldContainSubstring, `"wildcard"`)
			So(result, ShouldContainSubstring, `"exists"`)
		})
	})
}

func Test_NotLikeCond_Convert2SQL(t *testing.T) {
	Convey("Test NotLikeCond Convert2SQL", t, func() {
		ctx := context.Background()

		Convey("成功 - 转换SQL", func() {
			cond := &NotLikeCond{
				mCfg: &CondCfg{
					Value: "test",
				},
				mValue:           "test",
				mFilterFieldName: "name",
			}
			result, err := cond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"name"`)
			So(result, ShouldEqual, `"name" NOT LIKE 'test' ESCAPE '!'`)
		})
	})
}

func TestLikeCompletePatternAcrossOutputs(t *testing.T) {
	ctx := context.Background()
	patterns := []struct {
		value    string
		wildcard string
		sql      string
	}{
		{"apple", `"name": "apple"`, `"name" LIKE 'apple' ESCAPE '!'`},
		{"%apple%", `"name": "*apple*"`, `"name" LIKE '%apple%' ESCAPE '!'`},
		{`a\%_`, `"name": "a%?"`, `"name" LIKE 'a!%_' ESCAPE '!'`},
		{"a'b", `"name": "a'b"`, `"name" LIKE 'a''b' ESCAPE '!'`},
	}
	for _, tc := range patterns {
		t.Run(tc.value, func(t *testing.T) {
			like := &LikeCond{mValue: tc.value, mFilterFieldName: "name"}
			dsl, err := like.Convert(ctx, nil)
			if err != nil || !strings.Contains(dsl, tc.wildcard) {
				t.Fatalf("like DSL = %q, %v; want %q", dsl, err, tc.wildcard)
			}
			sql, err := like.Convert2SQL(ctx)
			if err != nil || sql != tc.sql {
				t.Fatalf("like SQL = %q, %v; want %q", sql, err, tc.sql)
			}
			notLike := &NotLikeCond{mValue: tc.value, mFilterFieldName: "name"}
			dsl, err = notLike.Convert(ctx, nil)
			if err != nil || !strings.Contains(dsl, tc.wildcard) || !strings.Contains(dsl, `"exists"`) {
				t.Fatalf("not_like DSL = %q, %v", dsl, err)
			}
			sql, err = notLike.Convert2SQL(ctx)
			if err != nil || sql != strings.Replace(tc.sql, " LIKE ", " NOT LIKE ", 1) {
				t.Fatalf("not_like SQL = %q, %v", sql, err)
			}
		})
	}
}

func TestRewriteLikePatternPreserved(t *testing.T) {
	field := &DataProperty{Name: "name", MappedField: Field{Name: "view_name"}}
	for _, operation := range []string{OperationLike, OperationNotLike} {
		cfg := &CondCfg{Name: "name", NameField: field, Operation: operation, Value: `%app\%le_`}
		var rewritten *CondCfg
		var err error
		if operation == OperationLike {
			rewritten, err = rewriteLikeCond(context.Background(), cfg)
		} else {
			rewritten, err = rewriteNotLikeCond(context.Background(), cfg)
		}
		if err != nil || rewritten.Name != "view_name" || rewritten.Value != cfg.Value {
			t.Fatalf("rewritten = %#v, %v", rewritten, err)
		}
		cfg.Value = `dangling\`
		if operation == OperationLike {
			_, err = rewriteLikeCond(context.Background(), cfg)
		} else {
			_, err = rewriteNotLikeCond(context.Background(), cfg)
		}
		if err == nil {
			t.Fatal("expected invalid pattern to be rejected before Vega")
		}
	}
}
