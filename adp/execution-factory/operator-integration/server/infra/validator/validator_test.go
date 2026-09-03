package validator

import (
	"context"
	"strings"
	"testing"

	validatorv10 "github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	. "github.com/smartystreets/goconvey/convey"
)

func TestValidateName(t *testing.T) {
	v := &validator{
		NameLimit: 50,
		Validator: validatorv10.New(),
	}
	ctx := context.Background()
	ctx = common.SetLanguageToCtx(ctx, common.SimplifiedChinese)
	var err error
	Convey("测试名称为空", t, func() {
		err = v.ValidateOperatorName(ctx, "")
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolName(ctx, "")
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolBoxName(ctx, "")
		So(err, ShouldNotBeNil)
	})
	Convey("测试名称长度超过限制", t, func() {
		err = v.ValidateOperatorName(ctx, strings.Repeat("a", 51))
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolName(ctx, strings.Repeat("a", 51))
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolBoxName(ctx, strings.Repeat("a", 51))
		So(err, ShouldNotBeNil)
	})
	Convey("ValidateOperatorName:测试名称合法性", t, func() {
		err = v.ValidateOperatorName(ctx, "aaa")
		So(err, ShouldBeNil)
		err = v.ValidateOperatorName(ctx, "中文")
		So(err, ShouldBeNil)
		err = v.ValidateOperatorName(ctx, "中文aa")
		So(err, ShouldBeNil)
		err = v.ValidateOperatorName(ctx, "中文 aa")
		So(err, ShouldNotBeNil)
		err = v.ValidateOperatorName(ctx, "中文_aa")
		So(err, ShouldBeNil)
		err = v.ValidateOperatorName(ctx, "中文$aa")
		So(err, ShouldNotBeNil)
		err = v.ValidateOperatorName(ctx, "中文@aa")
		So(err, ShouldNotBeNil)
		err = v.ValidateOperatorName(ctx, "中文^#aa")
		So(err, ShouldNotBeNil)
	})
	Convey("ValidatorToolBoxName:测试名称合法性", t, func() {
		err = v.ValidatorToolBoxName(ctx, "aaa")
		So(err, ShouldBeNil)
		err = v.ValidatorToolBoxName(ctx, "中文")
		So(err, ShouldBeNil)
		err = v.ValidatorToolBoxName(ctx, "中文aa")
		So(err, ShouldBeNil)
		err = v.ValidatorToolBoxName(ctx, "中文 aa")
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolBoxName(ctx, "中文_aa")
		So(err, ShouldBeNil)
		err = v.ValidatorToolBoxName(ctx, "中文$aa")
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolBoxName(ctx, "中文@aa")
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolBoxName(ctx, "中文^#aa")
		So(err, ShouldNotBeNil)
	})
	Convey("ValidatorToolName:测试名称合法性", t, func() {
		err = v.ValidatorToolName(ctx, "aaa")
		So(err, ShouldBeNil)
		err = v.ValidatorToolName(ctx, "中文")
		So(err, ShouldBeNil)
		err = v.ValidatorToolName(ctx, "中文aa")
		So(err, ShouldBeNil)
		err = v.ValidatorToolName(ctx, "中文 aa")
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolName(ctx, "中文_aa")
		So(err, ShouldBeNil)
		err = v.ValidatorToolName(ctx, "中文$aa")
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolName(ctx, "中文@aa")
		So(err, ShouldNotBeNil)
		err = v.ValidatorToolName(ctx, "中文^#aa")
		So(err, ShouldNotBeNil)
	})
}

func TestUUIDValidationAcceptsV4AndV7(t *testing.T) {
	type request struct {
		ID string `validate:"required,uuid"`
	}

	v := &validator{Validator: validatorv10.New()}
	v7ID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid.NewV7() error = %v", err)
	}
	v7 := v7ID.String()
	for _, id := range []string{"c2d8baf0-e31f-4cac-851d-30ad8c2e4722", v7} {
		if err := v.Validator.Struct(request{ID: id}); err != nil {
			t.Fatalf("UUID %q should pass validation: %v", id, err)
		}
	}
	if err := v.Validator.Struct(request{ID: "not-a-uuid"}); err == nil {
		t.Fatal("invalid UUID should fail validation")
	}
}
