// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package condition

import (
	"context"
	"testing"

	dtype "ontology-query/interfaces/data_type"

	. "github.com/smartystreets/goconvey/convey"
)

func Test_newAndCond(t *testing.T) {
	Convey("Test newAndCond", t, func() {
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

		Convey("成功 - 两个子条件", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
					{
						Name:      "age",
						Operation: OperationGt,
						ValueOptCfg: ValueOptCfg{
							Value: 18,
						},
					},
				},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})

		Convey("success - empty AND creates match-all condition", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds:  []*CondCfg{},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
			dsl, err := cond.Convert(ctx, nil)
			So(err, ShouldBeNil)
			So(dsl, ShouldContainSubstring, `"must"`)
		})

		Convey("失败 - 子条件超过限制", func() {
			subConds := make([]*CondCfg, MaxSubCondition+1)
			for i := 0; i <= MaxSubCondition; i++ {
				subConds[i] = &CondCfg{
					Name:      "name",
					Operation: OperationEq,
					ValueOptCfg: ValueOptCfg{
						Value: "test",
					},
				}
			}
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds:  subConds,
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})

		Convey("失败 - 子条件错误", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Name:      "nonexistent",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
				},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})
	})
}

func Test_AndCond_Convert(t *testing.T) {
	Convey("Test AndCond Convert", t, func() {
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

		Convey("成功 - 转换DSL", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
					{
						Name:      "age",
						Operation: OperationGt,
						ValueOptCfg: ValueOptCfg{
							Value: 18,
						},
					},
				},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			result, err := cond.Convert(ctx, nil)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"bool"`)
			So(result, ShouldContainSubstring, `"must"`)
		})
	})
}

func Test_AndCond_Convert2SQL(t *testing.T) {
	Convey("Test AndCond Convert2SQL", t, func() {
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

		Convey("成功 - 转换SQL", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
					{
						Name:      "age",
						Operation: OperationGt,
						ValueOptCfg: ValueOptCfg{
							Value: 18,
						},
					},
				},
			}
			cond, err := newAndCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			result, err := cond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `AND`)
		})
	})
}

func Test_rewriteAndCondition(t *testing.T) {
	Convey("Test rewriteAndCondition", t, func() {
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
		vectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
			return []VectorResp{}, nil
		}

		Convey("成功 - 重写AND条件", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
					{
						Name:      "age",
						Operation: OperationGt,
						ValueOptCfg: ValueOptCfg{
							Value: 18,
						},
					},
				},
			}
			// Set NameField.
			for _, subCond := range cfg.SubConds {
				if field, ok := fieldsMap[subCond.Name]; ok {
					subCond.NameField = field
				}
			}
			result, err := rewriteAndCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(len(result.SubConds), ShouldEqual, 2)
		})

		Convey("success - empty AND rewrites to nil", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds:  []*CondCfg{},
			}
			result, err := rewriteAndCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldBeNil)
		})

		Convey("success - empty nested AND is skipped during rewrite", func() {
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds: []*CondCfg{
					{
						Operation: OperationAnd,
						SubConds:  []*CondCfg{},
					},
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
				},
			}
			result, err := rewriteAndCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(len(result.SubConds), ShouldEqual, 1)
			So(result.SubConds[0].Name, ShouldEqual, "mapped_name")
		})

		Convey("失败 - 子条件超过限制", func() {
			subConds := make([]*CondCfg, MaxSubCondition+1)
			for i := 0; i <= MaxSubCondition; i++ {
				subConds[i] = &CondCfg{
					Name:      "name",
					Operation: OperationEq,
					ValueOptCfg: ValueOptCfg{
						Value: "test",
					},
				}
				subConds[i].NameField = fieldsMap["name"]
			}
			cfg := &CondCfg{
				Operation: OperationAnd,
				SubConds:  subConds,
			}
			result, err := rewriteAndCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})
	})
}

func Test_newOrCond(t *testing.T) {
	Convey("Test newOrCond", t, func() {
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

		Convey("成功 - 两个子条件", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds: []*CondCfg{
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test1",
						},
					},
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test2",
						},
					},
				},
			}
			cond, err := newOrCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
		})

		Convey("失败 - 子条件为空", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds:  []*CondCfg{},
			}
			cond, err := newOrCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			So(cond, ShouldBeNil)
		})

		Convey("success - nested empty AND makes OR match all", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds: []*CondCfg{
					{
						Operation: OperationAnd,
						SubConds:  []*CondCfg{},
					},
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
				},
			}
			cond, err := newOrCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			So(cond, ShouldNotBeNil)
			So(len(cond.(*OrCond).mSubConds), ShouldEqual, 2)
			dsl, err := cond.Convert(ctx, nil)
			So(err, ShouldBeNil)
			So(dsl, ShouldContainSubstring, `"should"`)
			So(dsl, ShouldContainSubstring, `"must"`)
		})
	})
}

func Test_OrCond_Convert(t *testing.T) {
	Convey("Test OrCond Convert", t, func() {
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

		Convey("成功 - 转换DSL", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds: []*CondCfg{
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test1",
						},
					},
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test2",
						},
					},
				},
			}
			cond, err := newOrCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			result, err := cond.Convert(ctx, nil)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `"bool"`)
			So(result, ShouldContainSubstring, `"should"`)
		})
	})
}

func Test_OrCond_Convert2SQL(t *testing.T) {
	Convey("Test OrCond Convert2SQL", t, func() {
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

		Convey("成功 - 转换SQL", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds: []*CondCfg{
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test1",
						},
					},
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test2",
						},
					},
				},
			}
			cond, err := newOrCond(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			result, err := cond.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(result, ShouldContainSubstring, `OR`)
		})
	})
}

func Test_rewriteOrCondition(t *testing.T) {
	Convey("Test rewriteOrCondition", t, func() {
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
		vectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
			return []VectorResp{}, nil
		}

		Convey("failure - empty OR fails closed during rewrite", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds:  []*CondCfg{},
			}
			result, err := rewriteOrCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})

		Convey("failure - nested empty OR fails closed during rewrite", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds: []*CondCfg{
					{
						Operation: OperationOr,
						SubConds:  []*CondCfg{},
					},
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
				},
			}
			result, err := rewriteOrCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldNotBeNil)
			So(result, ShouldBeNil)
		})

		Convey("success - nested empty AND makes OR rewrite to match all", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds: []*CondCfg{
					{
						Operation: OperationAnd,
						SubConds:  []*CondCfg{},
					},
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
				},
			}
			result, err := rewriteOrCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldBeNil)
		})

		Convey("success - nil child is skipped during OR rewrite", func() {
			cfg := &CondCfg{
				Operation: OperationOr,
				SubConds: []*CondCfg{
					nil,
					{
						Name:      "name",
						Operation: OperationEq,
						ValueOptCfg: ValueOptCfg{
							Value: "test",
						},
					},
				},
			}
			result, err := rewriteOrCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldBeNil)
			So(result, ShouldNotBeNil)
			So(len(result.SubConds), ShouldEqual, 1)
			So(result.SubConds[0].Name, ShouldEqual, "mapped_name")
		})
	})
}

func Test_EmptyConditionSetSemantics(t *testing.T) {
	Convey("Test empty AND / OR semantics across index DSL, rewrite and SQL", t, func() {
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
		vectorizer := func(ctx context.Context, property *DataProperty, word string) ([]VectorResp, error) {
			return []VectorResp{}, nil
		}
		leaf := func() *CondCfg {
			return &CondCfg{
				Name:        "name",
				Operation:   OperationEq,
				ValueOptCfg: ValueOptCfg{Value: "test"},
			}
		}
		emptyAnd := func() *CondCfg { return &CondCfg{Operation: OperationAnd, SubConds: []*CondCfg{}} }
		emptyOr := func() *CondCfg { return &CondCfg{Operation: OperationOr, SubConds: []*CondCfg{}} }

		Convey("empty AND matches all in index DSL and SQL", func() {
			c, err := NewCondition(ctx, emptyAnd(), CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			dsl, err := c.Convert(ctx, vectorizer)
			So(err, ShouldBeNil)
			So(dsl, ShouldContainSubstring, `"must": [`)
			sql, err := c.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldEqual, `1 = 1`)

			viewCfg, err := RewriteCondition(ctx, emptyAnd(), fieldsMap, vectorizer)
			So(err, ShouldBeNil)
			So(viewCfg, ShouldBeNil)
		})

		Convey("empty AND nested in OR yields valid SQL", func() {
			cfg := &CondCfg{Operation: OperationOr, SubConds: []*CondCfg{emptyAnd(), leaf()}}
			c, err := NewCondition(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			sql, err := c.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldEqual, `((1 = 1) OR ("name" = 'test'))`)

			viewCfg, err := RewriteCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldBeNil)
			So(viewCfg, ShouldBeNil)
		})

		Convey("empty AND nested in AND yields valid SQL", func() {
			cfg := &CondCfg{Operation: OperationAnd, SubConds: []*CondCfg{emptyAnd(), leaf()}}
			c, err := NewCondition(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldBeNil)
			sql, err := c.Convert2SQL(ctx)
			So(err, ShouldBeNil)
			So(sql, ShouldEqual, `1 = 1 AND "name" = 'test'`)
		})

		Convey("empty OR fails closed in index DSL and rewrite", func() {
			_, err := NewCondition(ctx, emptyOr(), CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			_, err = RewriteCondition(ctx, emptyOr(), fieldsMap, vectorizer)
			So(err, ShouldNotBeNil)
		})

		Convey("empty OR nested in AND fails closed in index DSL and rewrite", func() {
			cfg := &CondCfg{Operation: OperationAnd, SubConds: []*CondCfg{emptyOr(), leaf()}}
			_, err := NewCondition(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			_, err = RewriteCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldNotBeNil)
		})

		Convey("OR with only nil children fails closed in index DSL and rewrite", func() {
			cfg := &CondCfg{Operation: OperationOr, SubConds: []*CondCfg{nil}}
			_, err := NewCondition(ctx, cfg, CUSTOM, fieldsMap)
			So(err, ShouldNotBeNil)
			_, err = RewriteCondition(ctx, cfg, fieldsMap, vectorizer)
			So(err, ShouldNotBeNil)
		})
	})
}
