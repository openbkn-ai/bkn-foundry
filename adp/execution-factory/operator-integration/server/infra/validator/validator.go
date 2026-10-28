// Package validator definition interface.
// @file validator.go
// @description: Initialize validator.
package validator

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/asaskevich/govalidator"
	validatorv10 "github.com/go-playground/validator/v10"
	myErr "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const (
	defaultNameMaxLength = 50  // Maximum name length (characters).
	defaultDescMaxLength = 500 // Maximum description length (characters).
)

var (
	// Verification type.
	validParameterTypes = map[interfaces.ParameterType]bool{
		interfaces.ParameterTypeString:  true,
		interfaces.ParameterTypeNumber:  true,
		interfaces.ParameterTypeBoolean: true,
		interfaces.ParameterTypeArray:   true,
		interfaces.ParameterTypeObject:  true,
	}
)

// Validator validator interface.
type validator struct {
	Validator *validatorv10.Validate
	NameLimit int64 // Name length limit.
	DescLimit int64 // Description length limit.
}

var (
	vOnce sync.Once
	v     interfaces.Validator

	// Only supports Chinese, letters, numbers, and underscores.
	commonNameReg = `^[[:word:]\p{Han}]+$`
)

func NewValidator() interfaces.Validator {
	vOnce.Do(func() {
		v = &validator{
			Validator: validatorv10.New(),
			NameLimit: defaultNameMaxLength,
			DescLimit: defaultDescMaxLength,
		}
	})
	return v
}

// init initializes the validator.
func init() {
	validator := validatorv10.New()
	// Field name labels used by custom validators.
	validator.RegisterTagNameFunc(func(fld reflect.StructField) string {
		// Get the first value from the json tag of the struct field (other options ignored)
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0] //nolint:mnd

		// This field is skipped if label is set to "-".
		if name == "-" {
			return ""
		}
		// Returns the field name defined by the json tag.
		return name
	})
}

func (v *validator) ValidatorToolBoxName(ctx context.Context, name string) (err error) {
	if name == "" {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtToolBoxNameEmpty, "toolbox name cannot be empty")
		return
	}

	// Check length (calculated in number of characters)
	if utf8.RuneCountInString(name) > int(v.NameLimit) {
		err = fmt.Errorf("toolbox name %s length exceeds limit [%d]", name, v.NameLimit)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtToolBoxNameLimit, err.Error(),
			v.NameLimit)
		return
	}
	matched, _ := regexp.MatchString(commonNameReg, name)
	if !matched {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtCommonNameInvalid,
			fmt.Sprintf("toolbox name %s format is invalid", name))
	}
	return
}

func (v *validator) ValidatorToolBoxDesc(ctx context.Context, desc string) (err error) {
	// Toolbox description is not allowed to be empty.
	if desc == "" {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtToolBoxDescEmpty, "toolbox description cannot be empty")
		return
	}
	// Check length (calculated in number of characters)
	if utf8.RuneCountInString(desc) > int(v.DescLimit) {
		err = fmt.Errorf("toolbox description length exceeds limit [%d]", v.DescLimit)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtToolBoxDescLimit, err.Error(),
			v.DescLimit)
	}
	return
}
func (v *validator) ValidatorToolName(ctx context.Context, name string) (err error) {
	if name == "" {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtToolNameEmpty, "tool name cannot be empty")
		return
	}
	if utf8.RuneCountInString(name) > int(v.NameLimit) {
		err = fmt.Errorf("tool name length exceeds limit [%d]", v.NameLimit)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtToolNameLimit, err.Error(),
			v.NameLimit)
	}
	matched, _ := regexp.MatchString(commonNameReg, name)
	if !matched {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtCommonNameInvalid,
			fmt.Sprintf("tool name %s format is invalid", name))
	}
	return
}
func (v *validator) ValidatorToolDesc(ctx context.Context, desc string) (err error) {
	// Tool description is not allowed to be empty.
	if desc == "" {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtToolDescEmpty, "tool description cannot be empty")
		return
	}
	if utf8.RuneCountInString(desc) > int(v.DescLimit) {
		err = fmt.Errorf("tool description length exceeds limit [%d]", v.DescLimit)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtToolDescLimit, err.Error(),
			v.DescLimit)
	}
	return
}

func (v *validator) ValidatorMCPName(ctx context.Context, name string) (err error) {
	if name == "" {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtMCPNameEmpty, "mcp name cannot be empty")
		return
	}

	// Check length (calculated in number of characters)
	if utf8.RuneCountInString(name) > int(v.NameLimit) {
		err = fmt.Errorf("mcp name %s length exceeds limit [%d]", name, v.NameLimit)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtMCPNameLimit, err.Error(),
			v.NameLimit)
		return
	}
	matched, _ := regexp.MatchString(commonNameReg, name)
	if !matched {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtCommonNameInvalid,
			fmt.Sprintf("mcp name %s format is invalid", name))
	}
	return
}

func (v *validator) ValidatorMCPDesc(ctx context.Context, desc string) (err error) {
	if utf8.RuneCountInString(desc) > int(v.DescLimit) {
		err = fmt.Errorf("mcp description length exceeds limit [%d]", v.DescLimit)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtMCPDescLimit, err.Error(),
			v.DescLimit)
	}
	return
}

func (v *validator) ValidatorCategoryName(ctx context.Context, name string) (err error) {
	if name == "" {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtCategoryNameEmpty, "category name cannot be empty")
		return
	}

	// Check length (calculated in number of characters)
	if utf8.RuneCountInString(name) > int(v.NameLimit) {
		err = fmt.Errorf("category name %s length exceeds limit [%d]", name, v.NameLimit)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtCategoryNameLimit, err.Error(),
			v.NameLimit)
		return
	}
	matched, _ := regexp.MatchString(commonNameReg, name)
	if !matched {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtCommonNameInvalid,
			fmt.Sprintf("category name %s format is invalid", name))
	}
	return
}

// ValidatorStruct validation structure.
func (v *validator) ValidatorStruct(ctx context.Context, obj interface{}) (err error) {
	err = v.Validator.Struct(obj)
	if err == nil {
		return
	}
	vErr := make(validatorv10.ValidationErrors, 0)
	if !errors.As(err, &vErr) {
		return
	}
	extCode := TagToErrorType[vErr[0].Tag()]
	if extCode != "" {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, extCode, vErr[0].Error())
	}
	return
}

// Verify that the URL conforms to the format.
func (v *validator) ValidatorURL(ctx context.Context, url string) (err error) {
	if url == "" {
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtOpenAPIInvalidURLFormat, "URL cannot be empty")
		return
	}

	if !govalidator.IsURL(url) {
		err = fmt.Errorf("URL %s format is invalid", url)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtOpenAPIInvalidURLFormat, err.Error())
	}
	return
}

// VisitorParameterDef access parameter definition.
func (v *validator) VisitorParameterDef(ctx context.Context, paramDef *interfaces.ParameterDef) (err error) {
	if paramDef == nil {
		err = myErr.DefaultHTTPError(ctx, http.StatusBadRequest, "parameter def cannot be nil")
		return
	}

	if paramDef.Type != "" && !validParameterTypes[paramDef.Type] {
		err = fmt.Errorf("parameter %s type %s is invalid, must be string, number, boolean, array, object", paramDef.Name, paramDef.Type)
		err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtFunctionInvalidParameterType, err.Error(), paramDef.Name, paramDef.Type)
		return
	}

	// Verify that SubParameters can only be used for array and object types.
	if len(paramDef.SubParameters) > 0 {
		if paramDef.Type != "array" && paramDef.Type != "object" {
			err = fmt.Errorf("parameter %s type %s is invalid, must be array or object", paramDef.Name, paramDef.Type)
			err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtFunctionInvalidParameterSubParameters, err.Error(), paramDef.Name, paramDef.Type)
			return
		}

		// For array types, SubParameters should have only one element.
		if paramDef.Type == "array" && len(paramDef.SubParameters) != 1 {
			err = fmt.Errorf("parameter %s is array type, sub_parameters must only contain one element to define the structure of array items, current has %d elements",
				paramDef.Name, len(paramDef.SubParameters))
			err = myErr.NewHTTPError(ctx, http.StatusBadRequest, myErr.ErrExtFunctionInvalidParameterSubParametersCount, err.Error(), paramDef.Name, len(paramDef.SubParameters))
			return
		}

		// Recursively validate all subparameters.
		for _, subParam := range paramDef.SubParameters {
			if err = v.VisitorParameterDef(ctx, subParam); err != nil {
				return
			}
		}
	}
	if paramDef.Type == "array" && len(paramDef.SubParameters) == 0 {
		// Add default subparameters for array type.
		paramDef.SubParameters = []*interfaces.ParameterDef{
			{
				Type:        "string",
				Description: paramDef.Description,
				Required:    false,
			},
		}
	}
	return nil
}
