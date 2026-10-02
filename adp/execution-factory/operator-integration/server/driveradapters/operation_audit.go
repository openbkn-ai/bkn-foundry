package driveradapters

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/common/operationaudit"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/drivenadapters"
	infra "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/common"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const executionAuditRoutePrefix = "/api/agent-operator-integration/v1"
const executionPrivateAuditRoutePrefix = "/api/agent-operator-integration/internal-v1"
const maximumExecutionAuditRequestBody = 64 << 10

// OperationAudit records one minimal user-management fact only after the
// request finished. It does not persist request bodies, tool inputs or output.
func OperationAudit(recorder interface {
	Record(context.Context, operationaudit.Entry) error
}) gin.HandlerFunc {
	return operationAuditForSurface(recorder, false)
}

// OperationAuditPrivate records only internal management mutations. The
// private middleware's X-Account-ID is caller-supplied, so these records use
// anonymous/unknown attribution until a verifiable service identity exists.
func OperationAuditPrivate(recorder interface {
	Record(context.Context, operationaudit.Entry) error
}) gin.HandlerFunc {
	return operationAuditForSurface(recorder, true)
}

func operationAuditForSurface(recorder interface {
	Record(context.Context, operationaudit.Entry) error
}, private bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		prefix := executionAuditRoutePrefix
		if private {
			prefix = executionPrivateAuditRoutePrefix
		}
		path := strings.TrimPrefix(c.FullPath(), prefix)
		var rule operationAuditRule
		var ok bool
		if private {
			rule, ok = registeredPrivateOperationAudit(c.Request.Method, path)
		} else {
			rule, ok = registeredOperationAudit(c.Request.Method, path)
		}
		if !ok {
			c.Next()
			return
		}
		c.Request = c.Request.WithContext(operationaudit.WithManagementAuditOwner(c.Request.Context()))
		body := captureExecutionAuditRequest(c.Request)
		c.Next()
		if recorder == nil {
			return
		}
		auth, ok := infra.GetAccountAuthContextFromCtx(c.Request.Context())
		verifiedActor := !private && ok && auth != nil && strings.TrimSpace(auth.AccountID) != ""
		if !private && !verifiedActor && c.Writer.Status() != http.StatusUnauthorized && c.Writer.Status() != http.StatusForbidden {
			return
		}
		requestID := strings.TrimSpace(c.GetHeader(infra.HeaderBKNRequestID))
		if requestID == "" {
			requestID = executionAuditGeneratedRequestID()
			c.Request.Header.Set(infra.HeaderBKNRequestID, requestID)
			c.Header(infra.HeaderBKNRequestID, requestID)
		}
		actorID, actorName, actorType, authMethod := "anonymous", "", "anonymous", "unknown"
		if verifiedActor {
			actorID = auth.AccountID
			actorName = executionAuditActorName(c.Request.Context(), auth)
			actorType = executionAuditActorType(auth)
			authMethod = executionAuditAuthMethod(c.GetHeader("Authorization"))
		}
		targetID, targetName := rule.TargetType+":"+requestID, ""
		if verifiedActor {
			targetID, targetName = executionAuditTarget(c, rule.TargetType, body, requestID)
			if producerName := strings.TrimSpace(c.GetString(operationaudit.TargetNameContextKey)); producerName != "" {
				targetName = producerName
			}
		}
		if rule.TargetType == "import_batch" || rule.TargetType == "capability_bundle" {
			// These requests may touch multiple child resources; no single child ID
			// represents the entire attempt, including partial bundle success.
			targetID, targetName = rule.TargetType+":"+requestID, ""
		}
		if path == "/operator/convert/tool" {
			// This route creates a Tool from an existing Operator. A request's
			// operator_id is not the new Tool ID; failed attempts have no Tool ID.
			targetID = "tool:" + requestID
			if createdID := c.GetString(operationaudit.ToolIDContextKey); createdID != "" {
				targetID = createdID
			}
			targetName = targetID
		}
		outcome, failureCode, failureMessage := executionAuditOutcome(c.Writer.Status())
		if path == "/capabilities/openapi-bundle" && outcome == "success" && c.GetBool(operationaudit.PartialBundleContextKey) {
			outcome, failureCode, failureMessage = "unknown", "", ""
		}
		attemptID, err := uuid.NewV7()
		if err != nil {
			return
		}
		now := time.Now().UTC()
		sourceChannel := "api"
		if private {
			sourceChannel = "unknown"
		}
		entry := operationaudit.Entry{
			EventID:        attemptID.String(),
			EventTime:      now,
			RecordedAt:     now,
			ActorID:        actorID,
			ActorName:      actorName,
			ActorType:      actorType,
			AuthMethod:     authMethod,
			RequestID:      requestID,
			SourceChannel:  sourceChannel,
			Method:         c.Request.Method,
			HTTPStatus:     c.Writer.Status(),
			Action:         rule.Action,
			TargetType:     rule.TargetType,
			TargetID:       targetID,
			TargetName:     targetName,
			Outcome:        outcome,
			FailureCode:    failureCode,
			FailureMessage: failureMessage,
		}
		// The management result is never rolled back for audit failure.
		_ = recorder.Record(c.Request.Context(), entry)
	}
}

func executionAuditGeneratedRequestID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err == nil {
		return "req_" + hex.EncodeToString(bytes)
	}
	return fmt.Sprintf("req_%d", time.Now().UTC().UnixNano())
}

func captureExecutionAuditRequest(request *http.Request) map[string]any {
	if request == nil || request.Body == nil {
		return nil
	}
	// A declared oversized body is not audit input. Leave it untouched for the
	// business handler instead of consuming any of its stream.
	if request.ContentLength > maximumExecutionAuditRequestBody {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumExecutionAuditRequestBody+1))
	remaining := request.Body
	// For chunked requests the size is not known up front. Reassemble the
	// consumed prefix and unread suffix before returning, including on the
	// oversized path, so audit collection cannot change handler semantics.
	request.Body = struct {
		io.Reader
		io.Closer
	}{Reader: io.MultiReader(bytes.NewReader(body), remaining), Closer: remaining}
	if err != nil || len(body) > maximumExecutionAuditRequestBody {
		return nil
	}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return nil
	}
	return value
}

func executionAuditActorName(ctx context.Context, auth *interfaces.AccountAuthContext) string {
	if auth != nil && auth.TokenInfo != nil && strings.TrimSpace(auth.TokenInfo.VisitorName) != "" {
		return strings.TrimSpace(auth.TokenInfo.VisitorName)
	}
	if auth != nil && strings.TrimSpace(auth.AccountID) != "" {
		if user, err := drivenadapters.NewUserManagementClient().GetUserInfo(ctx, auth.AccountID, interfaces.DisplayName); err == nil && user != nil && strings.TrimSpace(user.DisplayName) != "" {
			return strings.TrimSpace(user.DisplayName)
		}
		return auth.AccountID
	}
	return "unknown"
}

func executionAuditActorType(auth *interfaces.AccountAuthContext) string {
	if auth != nil && auth.AccountType == interfaces.AccessorTypeApp {
		return "app"
	}
	return "user"
}

func executionAuditAuthMethod(authorization string) string {
	if strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")), interfaces.AppKeyPrefix) {
		return "api_key"
	}
	return "oauth"
}

func executionAuditTarget(c *gin.Context, targetType string, request map[string]any, requestID string) (string, string) {
	targetID := firstExecutionAuditNonEmpty(c.Param("operator_id"), c.Param("box_id"), c.Param("tool_id"), c.Param("mcp_id"), c.Param("skill_id"))
	if targetID == "" && request != nil {
		for _, key := range []string{"operator_id", "op_id", "box_id", "tool_id", "mcp_id", "skill_id", "id"} {
			if value, ok := request[key].(string); ok && strings.TrimSpace(value) != "" {
				targetID = strings.TrimSpace(value)
				break
			}
		}
	}
	if targetID == "" {
		targetID = targetType + ":" + requestID
	}
	name := ""
	if request != nil {
		for _, key := range []string{"name", "display_name", "operator_name", "box_name", "tool_name", "skill_name"} {
			if value, ok := request[key].(string); ok && strings.TrimSpace(value) != "" {
				name = strings.TrimSpace(value)
				break
			}
		}
	}
	if name == "" {
		name = targetID
	}
	return targetID, name
}

func executionAuditOutcome(status int) (string, string, string) {
	if status >= http.StatusOK && status < http.StatusBadRequest {
		return "success", "", ""
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return "denied", fmt.Sprintf("http_%d", status), "request denied"
	}
	return "failure", fmt.Sprintf("http_%d", status), "management request failed"
}

func firstExecutionAuditNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
