package aigeneration

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/localize"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

// Prompt template loading and parsing.

//go:embed templates/*.md templates/locales/*/*.md
var promptTemplatesFS embed.FS

var promptTemplateFiles = map[interfaces.PromptTemplateType]string{
	interfaces.PythonFunctionGenerator: "Python_Function_Generator.md",
	interfaces.MetadataParamGenerator:  "Metadata_Param_Generator.md",
}

// PromptLoader loads default and custom prompt templates.
type PromptLoader struct {
	DefaulTemplates    map[interfaces.PromptTemplateType]*interfaces.PromptTemplate
	MFModelManager     interfaces.MFModelManager
	AIGenerationConfig config.AIGenerationConfig
	Logger             interfaces.Logger
}

var (
	pOnce        sync.Once
	promptLoader *PromptLoader
)

// NewPromptLoader creates the shared prompt loader.
func NewPromptLoader() (*PromptLoader, error) {
	pOnce.Do(func() {
		conf := config.NewConfigLoader()
		promptLoader = &PromptLoader{
			Logger:             conf.GetLogger(),
			MFModelManager:     drivenadapters.NewMFModelManager(),
			AIGenerationConfig: conf.AIGenerationConfig,
			DefaulTemplates: map[interfaces.PromptTemplateType]*interfaces.PromptTemplate{
				interfaces.PythonFunctionGenerator: {
					Name: string(interfaces.PythonFunctionGenerator),
				},
				interfaces.MetadataParamGenerator: {
					Name:               string(interfaces.MetadataParamGenerator),
					UserPromptTemplate: `{"code": "%s", "inputs_json": %v, "outputs_json": %v}`,
				},
			},
		}
		// Load embedded default template files.
		if err := promptLoader.loadDefaulTemplates(); err != nil {
			panic(fmt.Errorf("failed to load prompt templates: %w", err))
		}
	})
	return promptLoader, nil
}

// loadDefaulTemplates loads embedded default template files.
func (l *PromptLoader) loadDefaulTemplates() error {
	for tempType, fileName := range promptTemplateFiles {
		content, err := fs.ReadFile(promptTemplatesFS, path.Join("templates", fileName))
		if err != nil {
			return fmt.Errorf("failed to read template file %s: %w", fileName, err)
		}
		l.DefaulTemplates[tempType].SystemPrompt = string(content)
	}
	return nil
}

// GetTemplate returns the configured template for a prompt type.
func (l *PromptLoader) GetTemplate(ctx context.Context, tempType interfaces.PromptTemplateType) (*interfaces.PromptTemplate, error) {
	temp, ok := l.DefaulTemplates[tempType]
	if !ok {
		return nil, fmt.Errorf("default prompt template %s not found", tempType)
	}
	temp = localizedPromptTemplate(ctx, tempType, temp)
	customPrompt, err := l.loadCustomPromptTemplate(ctx, tempType)
	if err != nil {
		l.Logger.WithContext(ctx).Warnf("failed to load custom prompt: %v", err)
	}
	if customPrompt == nil || customPrompt.SystemPrompt == "" {
		// Use the default template when no custom prompt is configured.
		return temp, nil
	}
	// Preserve the localized user prompt and description when using a custom system prompt.
	customPrompt.UserPromptTemplate = temp.UserPromptTemplate
	customPrompt.Description = temp.Description
	return customPrompt, nil
}

func localizedPromptTemplate(ctx context.Context, tempType interfaces.PromptTemplateType, source *interfaces.PromptTemplate) *interfaces.PromptTemplate {
	temp := *source
	tr := localize.NewI18nTranslator(common.GetLanguageFromCtx(ctx))
	switch tempType {
	case interfaces.PythonFunctionGenerator:
		temp.Description = tr.Trans("prompt.python_function_description")
		temp.UserPromptTemplate = tr.Trans("prompt.python_function_user_template")
	case interfaces.MetadataParamGenerator:
		temp.Description = tr.Trans("prompt.metadata_parameter_description")
	}
	if common.GetLanguageFromCtx(ctx) == common.AmericanEnglish {
		if fileName, ok := promptTemplateFiles[tempType]; ok {
			if content, err := fs.ReadFile(promptTemplatesFS, path.Join("templates", "locales", "en-US", fileName)); err == nil && len(content) > 0 {
				temp.SystemPrompt = string(content)
			}
		}
	}
	return &temp
}

// loadCustomPromptTemplate loads a custom prompt from the model manager.
func (l *PromptLoader) loadCustomPromptTemplate(ctx context.Context, tempType interfaces.PromptTemplateType) (*interfaces.PromptTemplate, error) {
	var promptID string
	switch tempType {
	case interfaces.PythonFunctionGenerator:
		promptID = l.AIGenerationConfig.PythonFunctionGeneratorPromptID
	case interfaces.MetadataParamGenerator:
		promptID = l.AIGenerationConfig.MetadataParamGeneratorPromptID
	}
	if promptID == "" {
		return nil, nil
	}
	// Load the prompt configuration from the model manager.
	promptResult, err := l.MFModelManager.GetPromptByPromptID(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("failed to get model config: %v", err)
	}
	l.Logger.WithContext(ctx).Debugf("model manager get prompt result: %v", promptResult)
	return &interfaces.PromptTemplate{
		PromptID:     promptResult.PromptID,
		Name:         promptResult.PromptName,
		SystemPrompt: promptResult.Messages,
	}, nil
}
