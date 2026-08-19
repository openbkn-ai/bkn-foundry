package common

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/config"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/rest"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/validator"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/aigeneration"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/auth"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	sharedrest "github.com/openbkn-ai/bkn-foundry/comm-go/rest"
)

// AIGenerationHandler AI generation processing interface.
type AIGenerationHandler interface {
	FunctionAIGeneration(c *gin.Context)
	// GetPromptTemplate Gets the prompt word template of the specified type.
	GetPromptTemplate(c *gin.Context)
}

type aiGenerationHandler struct {
	aiGenerationService interfaces.AIGenerationService
	Logger              interfaces.Logger
	Validator           interfaces.Validator
	AuthService         interfaces.IAuthorizationService
}

var (
	aiGenerationHandlerOnce sync.Once
	aiGenerationH           AIGenerationHandler
)

// NewAIGenerationHandler creates an AI generation processing interface instance.
func NewAIGenerationHandler() AIGenerationHandler {
	aiGenerationHandlerOnce.Do(func() {
		confLoader := config.NewConfigLoader()
		aiGenerationH = &aiGenerationHandler{
			aiGenerationService: aigeneration.NewAIGenerationService(),
			Logger:              confLoader.GetLogger(),
			Validator:           validator.NewValidator(),
			AuthService:         auth.NewAuthServiceImpl(),
		}
	})
	return aiGenerationH
}

// FunctionAIGeneration handles function AI generation requests.
//
// This interface calls a large model to generate function code and consume credits. In the public interface, the caller is required to hold create on the operator type.
// Permissions - Keep the same semantics as "the generated function will eventually be implemented as an operator" (see #345).
func (h *aiGenerationHandler) FunctionAIGeneration(c *gin.Context) {
	if err := requireOperatorTypePermission(c.Request.Context(), h.AuthService,
		interfaces.AuthOperationTypeCreate); err != nil {
		rest.ReplyError(c, err)
		return
	}
	req := &interfaces.FunctionAIGenerateReq{}
	if err := c.ShouldBindUri(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	if err := c.ShouldBindJSON(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	err := h.Validator.ValidatorStruct(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	err = req.Validate()
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusBadRequest, err.Error())
		rest.ReplyError(c, err)
		return
	}
	if !req.Stream {
		resp, err := h.aiGenerationService.FunctionAIGenerate(c.Request.Context(), req)
		if err != nil {
			rest.ReplyError(c, err)
			return
		}
		rest.ReplyOK(c, http.StatusOK, resp)
		return
	}
	messageChan, errorChan, err := h.aiGenerationService.FunctionAIGenerateStream(c.Request.Context(), req)
	if err != nil {
		err = errors.DefaultHTTPError(c.Request.Context(), http.StatusInternalServerError, err.Error())
		rest.ReplyError(c, err)
		return
	}
	// Set SSE response headers.
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "private, no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Access-Control-Allow-Headers", "Cache-Control")
	c.Header("Access-Control-Allow-Credentials", "false")
	// SSE response.
	var finish bool
	c.Stream(func(w io.Writer) bool {
		select {
		case msg, ok := <-messageChan:
			if !ok {
				return false // The message channel has been closed, ending the flow.
			}
			// Check if it is a closing tag.
			if isEndMarker(msg) {
				// Send SSE end tag.
				fmt.Fprintf(w, "%s\n\n", msg)
				flushIfSupported(w)
				return false
			}

			// Check the data in the data before forwarding. If it does not match the expected format, an error will be reported and the flow will end.
			if strings.HasPrefix(msg, "data:") {
				content := strings.TrimPrefix(msg, "data:") // Remove "data:" prefix.
				// Result expected format.
				result := &interfaces.ChatCompletionResp{}
				err = utils.StringToObject(content, result)
				if err != nil {
					// Prompt model exception and return error.
					h.Logger.WithContext(c.Request.Context()).Error(fmt.Sprintf("invalid SSE data format: %s, err: %s", content, err.Error()))
					err = errors.NewHTTPError(c.Request.Context(), http.StatusBadRequest, errors.ErrExtFunctionAIGenerateModelFailed, fmt.Sprintf("invalid SSE data format: %s, err: %s", content, err.Error()))
					c.SSEvent("error", utils.ObjectToJSON(err))
					flushIfSupported(w) // Ensure error messages are sent immediately.
					return false
				}
				if len(result.Choices) > 0 && result.Choices[0].FinishReason == "stop" {
					finish = true
				}
				// Check if there are choices.
				if !finish && len(result.Choices) == 0 && result.Model == "" && result.ID == "" && result.Object == "" {
					h.Logger.WithContext(c.Request.Context()).Error(fmt.Sprintf("invalid SSE data format: %s", content))
					err = errors.NewHTTPError(c.Request.Context(), http.StatusBadRequest, errors.ErrExtFunctionAIGenerateModelFailed, fmt.Sprintf("invalid SSE data format: %s", content))
					c.SSEvent("error", utils.ObjectToJSON(err))
					flushIfSupported(w) // Ensure error messages are sent immediately.
					return false
				}
			}
			fmt.Fprintf(w, "%s\n\n", msg)
			flushIfSupported(w)
			return true
		case err, ok := <-errorChan:
			if !ok {
				return false // Error channel closed, end stream.
			}
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				c.SSEvent("data", " [DONE]")
				flushIfSupported(w) // Make sure the last message is sent immediately.
				return false
			}
			if err != io.ErrUnexpectedEOF && err != io.EOF {
				errMsg := errors.DefaultHTTPError(c.Request.Context(), http.StatusInternalServerError, err.Error())
				c.SSEvent("error", utils.ObjectToJSON(errMsg))

				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			return false // An error occurred, ending the stream.
		case <-c.Request.Context().Done():
			// Client disconnected or request canceled.
			h.Logger.WithContext(c.Request.Context()).Info("SSE connection closed by client")
			return false
		}
	})
}

// flushIfSupported ensures data is sent immediately.
func flushIfSupported(w io.Writer) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// isEndMarker checks whether it is an end mark.
func isEndMarker(line string) bool {
	// Common closing tag patterns.
	endMarkers := []string{
		"data: [DONE]",
		"data: [END]",
		"data: DONE",
		"data: END",
		"[DONE]",
		"[END]",
	}

	for _, marker := range endMarkers {
		if strings.Contains(line, marker) {
			return true
		}
	}
	return false
}

// GetPromptTemplate Gets the prompt word template of the specified type.
func (h *aiGenerationHandler) GetPromptTemplate(c *gin.Context) {
	if err := requireOperatorTypePermission(c.Request.Context(), h.AuthService,
		interfaces.AuthOperationTypeCreate); err != nil {
		rest.ReplyError(c, err)
		return
	}
	req := &interfaces.GetPromptTemplateReq{}
	if err := c.ShouldBindUri(req); err != nil {
		rest.ReplyError(c, err)
		return
	}
	err := h.Validator.ValidatorStruct(c.Request.Context(), req)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	promptTemplate, err := h.aiGenerationService.GetPromptTemplate(c.Request.Context(), req.Type)
	if err != nil {
		rest.ReplyError(c, err)
		return
	}
	sharedrest.MarkLocalizedCacheableResponse(c)
	rest.ReplyOK(c, http.StatusOK, promptTemplate)
}
