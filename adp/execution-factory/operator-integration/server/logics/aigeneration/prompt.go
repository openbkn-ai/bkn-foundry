package aigeneration

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"sync"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
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
		content, err := i18n.Render(i18n.SimplifiedChinese, "AgentOperatorIntegration.SystemPrompt."+fileName, nil)
		if err != nil {
			return err
		}
		l.DefaulTemplates[tempType].SystemPrompt = content
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
	language := i18n.GetLanguageByCtx(ctx)
	switch tempType {
	case interfaces.PythonFunctionGenerator:
		text, renderErr := i18n.Render(language, "AgentOperatorIntegration.prompt.python_function_description", nil)
		if renderErr != nil {
			logger.Errorf("localized prompt failed: %v", renderErr)
		}
		temp.Description = text
		userPrompt, userRenderErr := i18n.Render(language, "AgentOperatorIntegration.prompt.python_function_user_template", nil)
		if userRenderErr != nil {
			logger.Errorf("localized prompt failed: %v", userRenderErr)
		}
		temp.UserPromptTemplate = userPrompt
	case interfaces.MetadataParamGenerator:
		metadataText, metadataRenderErr := i18n.Render(language, "AgentOperatorIntegration.prompt.metadata_parameter_description", nil)
		if metadataRenderErr != nil {
			logger.Errorf("localized prompt failed: %v", metadataRenderErr)
		}
		temp.Description = metadataText
	}
	if fileName, ok := promptTemplateFiles[tempType]; ok {
		text, renderErr := i18n.Render(language, "AgentOperatorIntegration.SystemPrompt."+fileName, nil)
		if renderErr != nil {
			logger.Errorf("localized system prompt failed: %v", renderErr)
		} else {
			temp.SystemPrompt = text
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

// init registers immutable prompt text; runtime language selection belongs to i18n.
func init() {
	for _, language := range []i18n.Language{i18n.SimplifiedChinese, i18n.AmericanEnglish} {
		messages := map[string]string{}
		for _, fileName := range promptTemplateFiles {
			resource := "templates/" + fileName
			if language == i18n.AmericanEnglish {
				resource = "templates/locales/en-US/" + fileName
			}
			content, err := fs.ReadFile(promptTemplatesFS, resource)
			if err != nil {
				panic(err)
			}
			messages["AgentOperatorIntegration.SystemPrompt."+fileName] = `{{printf "%s" ` + strconv.Quote(string(content)) + `}}`
		}
		i18n.RegisterI18n(&i18n.I18nMessages{Locale: language, Messages: messages})
	}
	if err := i18n.ValidateMessageBundles(); err != nil {
		panic(err)
	}
}
