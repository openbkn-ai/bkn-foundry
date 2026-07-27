package evidencevo

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const ArtifactContractVersion = "2.2.0"

type ArtifactType string

const (
	ArtifactTypeQuestion       ArtifactType = "question"
	ArtifactTypeResult         ArtifactType = "result"
	ArtifactTypeQuery          ArtifactType = "query"
	ArtifactTypeDataResult     ArtifactType = "data_result"
	ArtifactTypeLogicExecution ArtifactType = "logic_execution"
	ArtifactTypeActionInput    ArtifactType = "action_input"
	ArtifactTypeActionResult   ArtifactType = "action_result"
)

var supportedArtifactTypes = map[ArtifactType]struct{}{
	ArtifactTypeQuestion:       {},
	ArtifactTypeResult:         {},
	ArtifactTypeQuery:          {},
	ArtifactTypeDataResult:     {},
	ArtifactTypeLogicExecution: {},
	ArtifactTypeActionInput:    {},
	ArtifactTypeActionResult:   {},
}

var artifactHashPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var artifactSecretValuePattern = regexp.MustCompile(`(?i)(?:^|[\s,{])(?:authorization|cookie|password|secret|client[_-]?secret|token|(?:access|refresh|id)[_-]?token|api[_-]?key|private[_ -]?key)\s*[:=]\s*\S+`)
var opaqueArtifactLocationPattern = regexp.MustCompile(`^(?:artifact|snapshot):[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
var artifactIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

type ArtifactLinkRole struct {
	Field         string
	ExpectedTypes []ArtifactType
}

var artifactLinkRoles = map[string][]ArtifactLinkRole{
	"agent.interaction.started": {
		{Field: "question_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeQuestion}},
	},
	"data.query.observed": {
		{Field: "query_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeQuery}},
		{Field: "result_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeDataResult}},
	},
	"logic.execution.observed": {
		{Field: "input_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeDataResult}},
		{Field: "result_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeLogicExecution}},
	},
	"claim.created": {
		{Field: "result_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeResult}},
	},
	"action.recommended": {
		{Field: "reason_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeLogicExecution, ArtifactTypeResult}},
		{Field: "input_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeActionInput}},
	},
	"action.result_recorded": {
		{Field: "result_artifact_ref", ExpectedTypes: []ArtifactType{ArtifactTypeActionResult}},
	},
}

type EvidenceArtifact struct {
	ArtifactID     string       `json:"artifact_id"`
	ArtifactType   ArtifactType `json:"artifact_type"`
	RequestID      string       `json:"bkn.request.id"`
	TraceID        string       `json:"trace_id,omitempty"`
	InteractionID  string       `json:"interaction_id,omitempty"`
	OperationID    string       `json:"operation_id,omitempty"`
	ClaimID        string       `json:"claim_id,omitempty"`
	SourceRef      string       `json:"source_ref,omitempty"`
	BusinessRefs   []string     `json:"business_refs,omitempty"`
	ContentType    string       `json:"content_type"`
	SchemaVersion  string       `json:"schema_version"`
	ObservedAt     string       `json:"observed_at"`
	AsOf           string       `json:"as_of,omitempty"`
	SourceVersion  string       `json:"source_version,omitempty"`
	ContentHash    string       `json:"content_hash"`
	Content        any          `json:"content,omitempty"`
	SnapshotRef    string       `json:"snapshot_ref,omitempty"`
	TenantID       string       `json:"bkn.tenant.id,omitempty"`
	BusinessDomain string       `json:"business_domain,omitempty"`
	AccountID      string       `json:"bkn.account.id"`
	AccountType    string       `json:"bkn.account.type"`
	Initiator      string       `json:"initiator,omitempty"`
	AgentOrApp     string       `json:"agent_or_app,omitempty"`
}

type ArtifactIngestResponse struct {
	ArtifactID   string       `json:"artifact_id"`
	ArtifactType ArtifactType `json:"artifact_type"`
	RequestID    string       `json:"bkn.request.id"`
	TraceID      string       `json:"trace_id,omitempty"`
	ContentHash  string       `json:"content_hash"`
	Created      bool         `json:"created"`
}

func NormalizeArtifact(artifact EvidenceArtifact) (EvidenceArtifact, ValidationErrors) {
	rawArtifactID := artifact.ArtifactID
	artifact.ArtifactID = strings.TrimSpace(artifact.ArtifactID)
	artifact.RequestID = strings.TrimSpace(artifact.RequestID)
	artifact.TraceID = strings.TrimSpace(artifact.TraceID)
	artifact.InteractionID = strings.TrimSpace(artifact.InteractionID)
	artifact.OperationID = strings.TrimSpace(artifact.OperationID)
	artifact.ClaimID = strings.TrimSpace(artifact.ClaimID)
	artifact.SourceRef = strings.TrimSpace(artifact.SourceRef)
	artifact.ContentType = strings.TrimSpace(artifact.ContentType)
	artifact.SchemaVersion = strings.TrimSpace(artifact.SchemaVersion)
	artifact.ObservedAt = strings.TrimSpace(artifact.ObservedAt)
	artifact.AsOf = strings.TrimSpace(artifact.AsOf)
	artifact.SourceVersion = strings.TrimSpace(artifact.SourceVersion)
	artifact.ContentHash = strings.ToLower(strings.TrimSpace(artifact.ContentHash))
	artifact.SnapshotRef = strings.TrimSpace(artifact.SnapshotRef)
	artifact.TenantID = strings.TrimSpace(artifact.TenantID)
	artifact.BusinessDomain = strings.TrimSpace(artifact.BusinessDomain)
	artifact.AccountID = strings.TrimSpace(artifact.AccountID)
	artifact.AccountType = strings.TrimSpace(artifact.AccountType)
	artifact.Initiator = strings.TrimSpace(artifact.Initiator)
	artifact.AgentOrApp = strings.TrimSpace(artifact.AgentOrApp)

	var validationErrors ValidationErrors
	requireArtifactValue(artifact.ArtifactID, "artifact_id", &validationErrors)
	if rawArtifactID != artifact.ArtifactID || !ValidArtifactID(artifact.ArtifactID) {
		validationErrors = append(validationErrors, NewValidationError(
			"ARTIFACT_ID_INVALID", "artifact_id", "artifact_id must be 1..128 URL-safe characters without whitespace or slash",
		))
	}
	if _, ok := supportedArtifactTypes[artifact.ArtifactType]; !ok {
		validationErrors = append(validationErrors, NewValidationError(
			"ARTIFACT_TYPE_UNSUPPORTED", "artifact_type", "artifact_type is not supported",
		))
	}
	requireArtifactValue(artifact.RequestID, "bkn.request.id", &validationErrors)
	requireArtifactValue(artifact.ContentType, "content_type", &validationErrors)
	if artifact.SchemaVersion != ArtifactContractVersion {
		validationErrors = append(validationErrors, NewValidationError(
			"ARTIFACT_SCHEMA_UNSUPPORTED", "schema_version", "schema_version must be 2.2.0",
		))
	}
	if normalized, ok := CanonicalArtifactTimestamp(artifact.ObservedAt); !ok {
		validationErrors = append(validationErrors, NewValidationError(
			"ARTIFACT_TIMESTAMP_INVALID", "observed_at", "observed_at must be an RFC3339 timestamp",
		))
	} else {
		artifact.ObservedAt = normalized
	}
	if artifact.AsOf != "" {
		if normalized, ok := CanonicalArtifactTimestamp(artifact.AsOf); !ok {
			validationErrors = append(validationErrors, NewValidationError(
				"ARTIFACT_TIMESTAMP_INVALID", "as_of", "as_of must be an RFC3339 timestamp",
			))
		} else {
			artifact.AsOf = normalized
		}
	}
	requireArtifactValue(artifact.AccountID, "bkn.account.id", &validationErrors)
	requireArtifactValue(artifact.AccountType, "bkn.account.type", &validationErrors)
	if artifact.TenantID == "" && artifact.BusinessDomain == "" {
		validationErrors = append(validationErrors, NewValidationError(
			"ARTIFACT_OWNERSHIP_REQUIRED", "business_domain", "tenant or business domain is required",
		))
	}
	for _, item := range []struct {
		path  string
		value string
	}{
		{"artifact_id", artifact.ArtifactID},
		{"bkn.request.id", artifact.RequestID},
		{"trace_id", artifact.TraceID},
		{"interaction_id", artifact.InteractionID},
		{"operation_id", artifact.OperationID},
		{"claim_id", artifact.ClaimID},
		{"source_ref", artifact.SourceRef},
		{"content_type", artifact.ContentType},
		{"schema_version", artifact.SchemaVersion},
		{"observed_at", artifact.ObservedAt},
		{"as_of", artifact.AsOf},
		{"source_version", artifact.SourceVersion},
		{"snapshot_ref", artifact.SnapshotRef},
		{"bkn.tenant.id", artifact.TenantID},
		{"business_domain", artifact.BusinessDomain},
		{"bkn.account.id", artifact.AccountID},
		{"bkn.account.type", artifact.AccountType},
		{"initiator", artifact.Initiator},
		{"agent_or_app", artifact.AgentOrApp},
	} {
		scanArtifactString(item.value, item.path, &validationErrors)
	}
	for index, ref := range artifact.BusinessRefs {
		scanArtifactString(ref, fmt.Sprintf("business_refs[%d]", index), &validationErrors)
	}

	hasContent := artifact.Content != nil
	hasSnapshot := artifact.SnapshotRef != ""
	switch {
	case !hasContent && !hasSnapshot:
		validationErrors = append(validationErrors, NewValidationError(
			"ARTIFACT_CONTENT_REQUIRED", "content", "content or snapshot_ref is required",
		))
	case hasContent && hasSnapshot:
		validationErrors = append(validationErrors, NewValidationError(
			"ARTIFACT_CONTENT_AMBIGUOUS", "content", "content and snapshot_ref are mutually exclusive",
		))
	}

	if hasContent {
		scanArtifactValue(artifact.Content, "content", &validationErrors)
		canonical, err := marshalCanonicalArtifactContent(artifact.Content)
		if err != nil {
			validationErrors = append(validationErrors, NewValidationError(
				"ARTIFACT_CONTENT_INVALID", "content", "content must be valid JSON",
			))
		} else {
			sum := sha256.Sum256(canonical)
			computed := "sha256:" + hex.EncodeToString(sum[:])
			if artifact.ContentHash != "" && artifact.ContentHash != computed {
				validationErrors = append(validationErrors, NewValidationError(
					"ARTIFACT_CONTENT_HASH_MISMATCH", "content_hash", "content_hash does not match canonical content",
				))
			}
			artifact.ContentHash = computed
		}
	} else if hasSnapshot {
		if !opaqueArtifactLocationPattern.MatchString(artifact.SnapshotRef) {
			validationErrors = append(validationErrors, NewValidationError(
				"ARTIFACT_SNAPSHOT_REF_INVALID", "snapshot_ref", "snapshot_ref must be an opaque artifact:<id> or snapshot:<id> reference",
			))
		}
		if artifact.ContentHash == "" || !artifactHashPattern.MatchString(artifact.ContentHash) {
			validationErrors = append(validationErrors, NewValidationError(
				"ARTIFACT_CONTENT_HASH_REQUIRED", "content_hash", "snapshot artifacts require a sha256 content_hash",
			))
		}
	}

	return artifact, validationErrors
}

func marshalCanonicalArtifactContent(content any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(content); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

func MatchesArtifactScope(artifact EvidenceArtifact, scope QueryScope) bool {
	if artifact.AccountID == "" || artifact.AccountType == "" || artifact.TenantID == "" && artifact.BusinessDomain == "" {
		return false
	}
	if artifact.AccountID != scope.AccountID || artifact.AccountType != scope.AccountType {
		return false
	}
	if artifact.TenantID != "" && artifact.TenantID != scope.TenantID {
		return false
	}
	if artifact.BusinessDomain != "" && artifact.BusinessDomain != scope.BusinessDomain {
		return false
	}
	return true
}

func ArtifactFingerprint(artifact EvidenceArtifact) (string, error) {
	body, err := json.Marshal(artifact)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func requireArtifactValue(value, path string, validationErrors *ValidationErrors) {
	if value == "" {
		*validationErrors = append(*validationErrors, NewValidationError(
			"ARTIFACT_FIELD_REQUIRED", path, fmt.Sprintf("%s is required", path),
		))
	}
}

func CanonicalArtifactTimestamp(value string) (string, bool) {
	if value == "" {
		return "", false
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return "", false
	}
	return parsed.UTC().Format(time.RFC3339Nano), true
}

func ValidArtifactID(value string) bool {
	return artifactIDPattern.MatchString(value)
}

func ArtifactIDFromReference(value string) (string, bool) {
	if !strings.HasPrefix(value, "artifact:") {
		return "", false
	}
	artifactID := strings.TrimPrefix(value, "artifact:")
	return artifactID, ValidArtifactID(artifactID)
}

func ArtifactLinkRoles(eventType string) []ArtifactLinkRole {
	roles := artifactLinkRoles[eventType]
	result := make([]ArtifactLinkRole, len(roles))
	for index, role := range roles {
		result[index] = ArtifactLinkRole{
			Field:         role.Field,
			ExpectedTypes: append([]ArtifactType(nil), role.ExpectedTypes...),
		}
	}
	return result
}

func ArtifactTypeMatchesRole(artifactType ArtifactType, role ArtifactLinkRole) bool {
	for _, expectedType := range role.ExpectedTypes {
		if artifactType == expectedType {
			return true
		}
	}
	return false
}

func scanArtifactValue(value any, path string, validationErrors *ValidationErrors) {
	switch item := value.(type) {
	case map[string]any:
		for key, nested := range item {
			if forbiddenArtifactKey(key) {
				*validationErrors = append(*validationErrors, NewValidationError(
					"ARTIFACT_SECRET_FORBIDDEN", path+"."+key, "secret-bearing fields are forbidden",
				))
			}
			scanArtifactValue(nested, path+"."+key, validationErrors)
		}
	case []any:
		for index, nested := range item {
			scanArtifactValue(nested, fmt.Sprintf("%s[%d]", path, index), validationErrors)
		}
	case string:
		scanArtifactString(item, path, validationErrors)
	}
}

func forbiddenArtifactKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(key))
	switch normalized {
	case "token", "accesstoken", "refreshtoken", "idtoken", "cookie", "password", "passwd",
		"privatekey", "authorization", "apikey", "clientsecret", "secret", "secretkey":
		return true
	}
	return strings.HasSuffix(normalized, "token") ||
		strings.HasSuffix(normalized, "cookie") ||
		strings.HasSuffix(normalized, "password") ||
		strings.HasSuffix(normalized, "secret") ||
		strings.Contains(normalized, "privatekey") ||
		strings.Contains(normalized, "apikey") ||
		strings.Contains(normalized, "clientsecret")
}

func scanArtifactString(value, path string, validationErrors *ValidationErrors) {
	lower := strings.ToLower(strings.TrimSpace(value))
	if strings.Contains(value, "-----BEGIN PRIVATE KEY-----") ||
		strings.HasPrefix(lower, "bearer ") ||
		artifactSecretValuePattern.MatchString(value) ||
		credentialBearingURL(value) {
		*validationErrors = append(*validationErrors, NewValidationError(
			"ARTIFACT_SECRET_FORBIDDEN", path, "secret values are forbidden",
		))
	}
	if bareObjectStorageURL(lower) {
		*validationErrors = append(*validationErrors, NewValidationError(
			"ARTIFACT_STORAGE_URL_FORBIDDEN", path, "bare object storage URLs are forbidden; use an opaque snapshot_ref",
		))
	}
}

func credentialBearingURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.User != nil {
		return true
	}
	for key := range parsed.Query() {
		normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(key))
		if forbiddenArtifactKey(key) {
			return true
		}
		switch normalized {
		case "signature", "sig", "credential", "xamzcredential", "xamzsignature", "xgoogsignature":
			return true
		}
	}
	return false
}

func bareObjectStorageURL(value string) bool {
	for _, prefix := range []string{"s3://", "oss://", "cos://", "obs://", "gs://", "azure://"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	for _, hostMarker := range []string{
		".s3.amazonaws.com/",
		".s3.",
		".oss-",
		".aliyuncs.com/",
		".cos.",
		".myqcloud.com/",
		".obs.",
	} {
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			if strings.Contains(value, hostMarker) {
				return true
			}
		}
	}
	return false
}
