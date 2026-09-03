// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.

package driveradapters

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/openbkn-ai/bkn-foundry/comm-go/hydra"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"

	"bkn-backend/common/operationaudit"
	"bkn-backend/interfaces"
)

const (
	operationAuditVisitorKey = "bkn.operation_audit.visitor"
	operationAuditRequestKey = "bkn.operation_audit.request_id"
)

const maximumOperationAuditBody = 64 << 10

type operationAuditRule struct {
	Action     string
	TargetType string
}

type operationAuditActor struct {
	ActorID      string
	ActorName    string
	ActorType    string
	AuthMethod   string
	CredentialID string
}

type operationAuditRecorder interface {
	Record(context.Context, operationaudit.Entry) error
}

type operationAuditQueryStore interface {
	List(context.Context, operationaudit.Filter) (operationaudit.Page, error)
	Get(context.Context, string, operationaudit.Scope) (operationaudit.Entry, bool, error)
}

var operationAuditRoutes = map[string]operationAuditRule{
	"POST /knowledge-networks":                                                           {Action: "create", TargetType: "knowledge_network"},
	"PUT /knowledge-networks/:kn_id":                                                     {Action: "update", TargetType: "knowledge_network"},
	"DELETE /knowledge-networks/:kn_id":                                                  {Action: "delete", TargetType: "knowledge_network"},
	"POST /knowledge-networks/:kn_id/object-types":                                       {Action: "create", TargetType: "object_type"},
	"PUT /knowledge-networks/:kn_id/object-types/:ot_id":                                 {Action: "update", TargetType: "object_type"},
	"PUT /knowledge-networks/:kn_id/object-types/:ot_id/data_properties/:property_names": {Action: "update", TargetType: "object_type"},
	"DELETE /knowledge-networks/:kn_id/object-types/:ot_ids":                             {Action: "delete", TargetType: "object_type"},
	"POST /knowledge-networks/:kn_id/relation-types":                                     {Action: "create", TargetType: "relation_type"},
	"PUT /knowledge-networks/:kn_id/relation-types/:rt_id":                               {Action: "update", TargetType: "relation_type"},
	"DELETE /knowledge-networks/:kn_id/relation-types/:rt_ids":                           {Action: "delete", TargetType: "relation_type"},
	"POST /knowledge-networks/:kn_id/action-types":                                       {Action: "create", TargetType: "action_type"},
	"PUT /knowledge-networks/:kn_id/action-types/:at_id":                                 {Action: "update", TargetType: "action_type"},
	"DELETE /knowledge-networks/:kn_id/action-types/:at_ids":                             {Action: "delete", TargetType: "action_type"},
	"POST /knowledge-networks/:kn_id/metrics":                                            {Action: "create", TargetType: "metric"},
	"PUT /knowledge-networks/:kn_id/metrics/:metric_ids":                                 {Action: "update", TargetType: "metric"},
	"DELETE /knowledge-networks/:kn_id/metrics/:metric_ids":                              {Action: "delete", TargetType: "metric"},
	"POST /knowledge-networks/:kn_id/risk-types":                                         {Action: "create", TargetType: "risk_type"},
	"PUT /knowledge-networks/:kn_id/risk-types/:rt_id":                                   {Action: "update", TargetType: "risk_type"},
	"DELETE /knowledge-networks/:kn_id/risk-types/:rt_ids":                               {Action: "delete", TargetType: "risk_type"},
	"POST /knowledge-networks/:kn_id/concept-groups":                                     {Action: "create", TargetType: "concept_group"},
	"PUT /knowledge-networks/:kn_id/concept-groups/:cg_id":                               {Action: "update", TargetType: "concept_group"},
	"DELETE /knowledge-networks/:kn_id/concept-groups/:cg_id":                            {Action: "delete", TargetType: "concept_group"},
	"POST /knowledge-networks/:kn_id/concept-groups/:cg_id/object-types":                 {Action: "add_members", TargetType: "concept_group"},
	"DELETE /knowledge-networks/:kn_id/concept-groups/:cg_id/object-types/:ot_ids":       {Action: "remove_members", TargetType: "concept_group"},
	"POST /knowledge-networks/:kn_id/action-schedules":                                   {Action: "create", TargetType: "action_schedule"},
	"PUT /knowledge-networks/:kn_id/action-schedules/:schedule_id":                       {Action: "update", TargetType: "action_schedule"},
	"PUT /knowledge-networks/:kn_id/action-schedules/:schedule_id/status":                {Action: "update", TargetType: "action_schedule"},
	"DELETE /knowledge-networks/:kn_id/action-schedules/:schedule_ids":                   {Action: "delete", TargetType: "action_schedule"},
	"POST /bkns": {Action: "import", TargetType: "knowledge_network"},
}

func registeredOperationAudit(method, fullPath, methodOverride string) (operationAuditRule, bool) {
	if strings.EqualFold(strings.TrimSpace(methodOverride), "GET") {
		return operationAuditRule{}, false
	}
	path := strings.TrimSpace(fullPath)
	for _, prefix := range []string{
		"/api/bkn-backend/v1", "/api/ontology-manager/v1",
		"/api/bkn-backend/in/v1", "/api/ontology-manager/in/v1",
	} {
		if strings.HasPrefix(path, prefix) {
			path = strings.TrimPrefix(path, prefix)
			break
		}
	}
	rule, ok := operationAuditRoutes[strings.ToUpper(strings.TrimSpace(method))+" "+path]
	return rule, ok
}

func (r *restHandler) operationAuditIdentity(ctx context.Context, authorization string, visitor hydra.Visitor) operationAuditActor {
	if r != nil && r.auditIdentityResolver != nil {
		identity := r.auditIdentityResolver(ctx, authorization, visitor)
		if identity.ActorID != "" && identity.ActorName != "" {
			return identity
		}
	}
	return basicOperationAuditActor(authorization, visitor)
}

func basicOperationAuditActor(authorization string, visitor hydra.Visitor) operationAuditActor {
	authMethod := "oauth"
	if strings.TrimSpace(authorization) == "" {
		authMethod = "service_header"
	} else if strings.Contains(strings.ToLower(authorization), "bak_") {
		authMethod = "app_key"
	}
	actorType := strings.TrimSpace(string(visitor.Type))
	if actorType == "" {
		actorType = "user"
	}
	return operationAuditActor{
		ActorID: strings.TrimSpace(visitor.ID), ActorName: strings.TrimSpace(visitor.ID),
		ActorType: actorType, AuthMethod: authMethod, CredentialID: strings.TrimSpace(visitor.TokenID),
	}
}

// OperationAudit records exactly one bounded management fact after a registered
// request attempt. It never changes the business response when audit persistence
// fails; the loss is explicit in the service log instead of being silently hidden.
func (r *restHandler) OperationAudit() gin.HandlerFunc {
	return func(c *gin.Context) {
		rule, registered := registeredOperationAudit(c.Request.Method, c.FullPath(), c.GetHeader(interfaces.HTTP_HEADER_METHOD_OVERRIDE))
		if !registered {
			c.Next()
			return
		}
		requestBody := captureRequestBody(c.Request)
		responseBody := &boundedResponseWriter{ResponseWriter: c.Writer, limit: maximumOperationAuditBody}
		c.Writer = responseBody
		c.Next()
		responseBody.FlushResponse()

		if r == nil || r.auditRecorder == nil {
			return
		}
		requestID, err := operationAuditRequestID(c)
		if err != nil {
			logger.Errorf("operation audit request ID generation failed: action=%s target_type=%s error=%v", rule.Action, rule.TargetType, err)
			return
		}
		visitor, _ := operationAuditVisitor(c)
		actor := r.operationAuditIdentity(c.Request.Context(), c.GetHeader("Authorization"), visitor)
		if actor.ActorID == "" {
			actor.ActorID, actor.ActorName, actor.ActorType = "unauthenticated", "未认证调用者", "unknown"
		}
		facts := operationAuditFacts(c, rule, requestBody, responseBody.body.Bytes(), requestID)
		now := time.Now().UTC()
		entry := operationaudit.Entry{
			EventID:            operationAuditEventID(requestID, c.Request.Method, c.Request.URL.Path),
			EventTime:          now,
			RecordedAt:         now,
			KnowledgeNetworkID: facts.knowledgeNetworkID,
			ActorID:            actor.ActorID,
			ActorName:          actor.ActorName,
			ActorType:          actor.ActorType,
			AuthMethod:         actor.AuthMethod,
			CredentialID:       actor.CredentialID,
			RequestID:          requestID,
			SourceChannel:      operationAuditSourceChannel(c.FullPath()),
			Method:             c.Request.Method,
			Action:             rule.Action,
			TargetType:         rule.TargetType,
			TargetID:           facts.targetID,
			TargetName:         facts.targetName,
			Outcome:            facts.outcome,
			FailureCode:        facts.failureCode,
			FailureMessage:     facts.failureMessage,
			ChangeSummary:      facts.changeSummary,
		}
		if entry.KnowledgeNetworkID == "" {
			entry.KnowledgeNetworkID = entry.TargetID
		}
		if err := r.auditRecorder.Record(c.Request.Context(), entry); err != nil {
			logger.Errorf("operation audit persistence failed: request_id=%s action=%s target_type=%s error=%v", entry.RequestID, entry.Action, entry.TargetType, err)
		}
	}
}

func operationAuditEventID(requestID, method, path string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{requestID, strings.ToUpper(method), path}, "\n")))
	return fmt.Sprintf("evt_%x", sum[:])
}

type boundedResponseWriter struct {
	gin.ResponseWriter
	body        bytes.Buffer
	limit       int
	status      int
	wroteHeader bool
}

func (w *boundedResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
}

func (w *boundedResponseWriter) WriteHeaderNow() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
}

func (w *boundedResponseWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	_, err := w.body.Write(data)
	return len(data), err
}

func (w *boundedResponseWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *boundedResponseWriter) FlushResponse() {
	if w.wroteHeader && w.ResponseWriter.Written() {
		return
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	payload, ok := marshalJSONResponse(w.body.Bytes())
	if !ok {
		status = http.StatusInternalServerError
		payload = []byte(`{"error":"invalid JSON response"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.ResponseWriter.WriteHeader(status)
	_, _ = w.ResponseWriter.Write(payload)
}

func marshalJSONResponse(data []byte) ([]byte, bool) {
	if len(data) == 0 {
		return nil, true
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, false
	}
	encoded, err := json.Marshal(value)
	return encoded, err == nil
}

func captureRequestBody(request *http.Request) []byte {
	if request == nil || request.Body == nil {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maximumOperationAuditBody+1))
	if err != nil {
		return nil
	}
	request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), request.Body))
	if len(body) > maximumOperationAuditBody {
		return nil
	}
	return body
}

func operationAuditVisitor(c *gin.Context) (hydra.Visitor, bool) {
	value, exists := c.Get(operationAuditVisitorKey)
	if exists {
		visitor, ok := value.(hydra.Visitor)
		if ok {
			return visitor, true
		}
	}
	if operationAuditInternalRoute(c.FullPath()) {
		return hydra.Visitor{
			ID:   strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_ACCOUNT_ID)),
			Type: hydra.VisitorType(strings.TrimSpace(c.GetHeader(interfaces.HTTP_HEADER_ACCOUNT_TYPE))),
		}, true
	}
	return hydra.Visitor{}, false
}

func operationAuditInternalRoute(fullPath string) bool {
	path := strings.TrimSpace(fullPath)
	return strings.HasPrefix(path, "/api/bkn-backend/in/v1/") ||
		strings.HasPrefix(path, "/api/ontology-manager/in/v1/")
}

type extractedOperationAuditFacts struct {
	knowledgeNetworkID string
	targetID           string
	targetName         string
	outcome            string
	failureCode        string
	failureMessage     string
	changeSummary      map[string]any
}

func operationAuditFacts(c *gin.Context, rule operationAuditRule, requestBody, responseBody []byte, requestID string) extractedOperationAuditFacts {
	requestJSON := decodeBoundedObject(requestBody)
	responseJSON := decodeBoundedObject(responseBody)
	knID := strings.TrimSpace(c.Param("kn_id"))
	targetID := operationAuditPathTarget(c, rule.TargetType)
	targetName := ""
	requestIDs, requestNames := targetValues(requestJSON)
	responseIDs, responseNames := targetValues(responseJSON)
	if len(responseIDs) > 0 {
		targetID = strings.Join(responseIDs, ",")
	} else if targetID == "" && len(requestIDs) > 0 {
		targetID = strings.Join(requestIDs, ",")
	}
	if len(responseNames) > 0 {
		targetName = strings.Join(responseNames, "、")
	} else if len(requestNames) > 0 {
		targetName = strings.Join(requestNames, "、")
	}
	if targetID == "" {
		targetID = rule.TargetType + ":" + requestID
	}
	if targetName == "" {
		targetName = targetID
	}
	if knID == "" && rule.TargetType == "knowledge_network" {
		knID = targetID
	}
	outcome := "success"
	failureCode, failureMessage := "", ""
	if c.Writer.Status() < http.StatusOK || c.Writer.Status() >= http.StatusBadRequest {
		outcome = "failure"
		if c.Writer.Status() == http.StatusUnauthorized || c.Writer.Status() == http.StatusForbidden {
			outcome = "denied"
		}
		failureCode, failureMessage = boundedFailure(responseJSON, c.Writer.Status())
	}
	return extractedOperationAuditFacts{
		knowledgeNetworkID: knID,
		targetID:           boundText(targetID, 1024), targetName: boundText(targetName, 1024),
		outcome: outcome, failureCode: failureCode, failureMessage: failureMessage,
		changeSummary: changedFields(requestJSON),
	}
}

func operationAuditPathTarget(c *gin.Context, targetType string) string {
	names := map[string][]string{
		"knowledge_network": {"kn_id"}, "object_type": {"ot_id", "ot_ids"},
		"relation_type": {"rt_id", "rt_ids"}, "action_type": {"at_id", "at_ids"},
		"risk_type": {"rt_id", "rt_ids"},
		"metric":    {"metric_ids"}, "concept_group": {"cg_id"}, "action_schedule": {"schedule_id", "schedule_ids"},
	}
	for _, name := range names[targetType] {
		if value := strings.TrimSpace(c.Param(name)); value != "" {
			return value
		}
	}
	return ""
}

func decodeBoundedObject(body []byte) map[string]any {
	if len(body) == 0 {
		return map[string]any{}
	}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return map[string]any{}
	}
	return value
}

func targetValues(value map[string]any) ([]string, []string) {
	ids, names := []string{}, []string{}
	appendObject := func(object map[string]any) {
		if id := firstNonEmpty(stringValue(object["id"]), stringValue(object["kn_id"])); id != "" {
			ids = append(ids, id)
		}
		if name := stringValue(object["name"]); name != "" {
			names = append(names, name)
		}
	}
	appendObject(value)
	if entries, ok := value["entries"].([]any); ok {
		for _, item := range entries {
			if object, ok := item.(map[string]any); ok {
				appendObject(object)
			}
		}
	}
	return uniqueStrings(ids), uniqueStrings(names)
}

func changedFields(value map[string]any) map[string]any {
	allowed := map[string]struct{}{
		"name": {}, "comment": {}, "tags": {}, "icon": {}, "color": {}, "branch": {},
		"status": {}, "entries": {}, "object_type_ids": {},
		"cron_expression": {}, "action_type_id": {},
	}
	fields := []string{}
	for key := range value {
		if _, ok := allowed[key]; ok {
			fields = append(fields, key)
		}
	}
	if entries, ok := value["entries"].([]any); ok {
		for _, item := range entries {
			if object, ok := item.(map[string]any); ok {
				for key := range object {
					if _, allowedKey := allowed[key]; allowedKey && key != "entries" {
						fields = append(fields, key)
					}
				}
			}
		}
	}
	fields = uniqueStrings(fields)
	sort.Strings(fields)
	summary := map[string]any{"changed_fields": fields}
	if status := stringValue(value["status"]); status != "" {
		summary["status"] = boundText(status, 64)
	}
	return summary
}

func boundedFailure(value map[string]any, status int) (string, string) {
	code := http.StatusText(status)
	message := code
	if nested, ok := value["error"].(map[string]any); ok {
		code = firstNonEmpty(stringValue(nested["code"]), code)
		message = firstNonEmpty(stringValue(nested["message"]), stringValue(nested["description"]), message)
	} else {
		code = firstNonEmpty(stringValue(value["code"]), code)
		message = firstNonEmpty(stringValue(value["message"]), stringValue(value["description"]), message)
	}
	return boundText(code, 128), boundText(message, 512)
}

func operationAuditRequestID(c *gin.Context) (string, error) {
	if value, exists := c.Get(operationAuditRequestKey); exists {
		if requestID, ok := value.(string); ok && requestID != "" {
			return requestID, nil
		}
	}
	requestID := firstNonEmptyHeader(c, headerBKNRequestID, headerLegacyRequestID)
	if len(requestID) > 128 || !operationAuditPrintableASCII(requestID) {
		requestID = ""
	}
	if requestID == "" {
		id, err := uuid.NewV7()
		if err != nil {
			return "", fmt.Errorf("generate operation audit request UUIDv7: %w", err)
		}
		requestID = "req_" + id.String()
	}
	c.Request.Header.Set(headerBKNRequestID, requestID)
	c.Set(operationAuditRequestKey, requestID)
	c.Header(headerBKNRequestID, requestID)
	return requestID, nil
}

func operationAuditPrintableASCII(value string) bool {
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func operationAuditScopeValue(headerValue, environmentName string) string {
	return firstNonEmpty(strings.TrimSpace(headerValue), strings.TrimSpace(os.Getenv(environmentName)))
}

func operationAuditSourceChannel(fullPath string) string {
	// Internal and external HTTP routes share the public "api" channel. The
	// authentication method distinguishes service calls without expanding the
	// cross-service source_channel contract.
	return "api"
}

func stringValue(value any) string {
	stringValue, _ := value.(string)
	return strings.TrimSpace(stringValue)
}

func boundText(value string, maximum int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
