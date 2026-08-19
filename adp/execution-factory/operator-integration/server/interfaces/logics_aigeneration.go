// Package interfaces define interfaces.
// @file logics_aigeneration.go
// @description: AI generation related interfaces.
package interfaces

import (
	"context"
	"fmt"
)

// PromptTemplate prompt word template structure.
type PromptTemplate struct {
	PromptID           string `json:"prompt_id"`
	Name               string `json:"name" validate:"required"`
	Description        string `json:"description" validate:"required"`
	SystemPrompt       string `json:"system_prompt" validate:"required"`
	UserPromptTemplate string `json:"user_prompt_template" validate:"required"`
}

// FormatUserPrompt format user prompt word.
func (p *PromptTemplate) FormatUserPrompt(args ...interface{}) string {
	return fmt.Sprintf(p.UserPromptTemplate, args...)
}

// PromptTemplateType prompt word template type.
type PromptTemplateType string

const (
	PythonFunctionGenerator PromptTemplateType = "python_function_generator" // Python function generates Prompt template.
	MetadataParamGenerator  PromptTemplateType = "metadata_param_generator"  // Metadata parameters generate Prompt template.
)

// FunctionAIGenerateReq Function AI generation request.
type FunctionAIGenerateReq struct {
	Type    PromptTemplateType `uri:"type" validate:"required,oneof=python_function_generator metadata_param_generator"` // Prompt word template type, required.
	Query   string             `json:"query"`                                                                            // User input, required.
	Inputs  []ParameterDef     `json:"inputs,omitempty" form:"inputs"`                                                   // Input parameter list.
	Outputs []ParameterDef     `json:"outputs,omitempty" form:"outputs"`
	Code    string             `json:"code,omitempty" form:"code"`     // Output parameter list.
	Stream  bool               `json:"stream,omitempty" form:"stream"` // Whether to stream the return.
}

// Validate verify request parameters.
func (f *FunctionAIGenerateReq) Validate() error {
	switch f.Type {
	case PythonFunctionGenerator:
		if f.Query == "" {
			return fmt.Errorf("query is empty, please input a valid query")
		}
	case MetadataParamGenerator:
		if f.Code == "" {
			return fmt.Errorf("code is empty, please input a valid code")
		}
	default:
		return fmt.Errorf("template type %s is not supported, only support python_function_generator, metadata_param_generator, metadata_test_data_generator", f.Type)
	}
	return nil
}

// FunctionAIGeneratResp function intelligently generates responses.
type FunctionAIGeneratResp struct {
	Content any `json:"content"` // Generate content.
}

// AIGeneratMetadataContent AI generated metadata content.
type AIGeneratMetadataContent struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	UseRule     string         `json:"use_rule"`
	Inputs      []ParameterDef `json:"inputs"`
	Outputs     []ParameterDef `json:"outputs"`
}

// AIGenerationService AI-assisted generation interface.
type AIGenerationService interface {
	// FunctionAIGenerate function intelligent generation.
	FunctionAIGenerate(ctx context.Context, req *FunctionAIGenerateReq) (*FunctionAIGeneratResp, error)
	// FunctionAIGenerateStream function intelligently generates streaming returns.
	FunctionAIGenerateStream(ctx context.Context, req *FunctionAIGenerateReq) (respChan chan string, errChan chan error, err error)
	// GetPromptTemplate Gets the prompt word template of the specified type.
	GetPromptTemplate(ctx context.Context, tempType PromptTemplateType) (*PromptTemplate, error)
}

// GetPromptTemplateReq Gets the prompt word template request.
type GetPromptTemplateReq struct {
	Type PromptTemplateType `uri:"type" validate:"required,oneof=python_function_generator metadata_param_generator"` // Prompt word template type, required.
}
