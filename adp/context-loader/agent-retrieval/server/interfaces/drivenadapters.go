// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

// Package interfaces defines interfaces
// @file drivenadapters.go
// @description: Inbound interface definition
package interfaces

//go:generate mockgen -source=drivenadapters.go -destination=../mocks/drivenadapters.go -package=mocks
import (
	"context"
)

// AccountAuthContext Account authentication context
type AccountAuthContext struct {
	// AccountID Account unique identifier
	AccountID string `json:"account_id"`
	// AccountType Account Type
	AccountType AccessorType `json:"account_type"`
	// AuthMethod records the trusted credential class used at the public boundary.
	AuthMethod string `json:"auth_method,omitempty"`
	// TokenInfo Token information
	TokenInfo *TokenInfo `json:"token_info"`
}

// VisitorType Visitor Type
type VisitorType string

// Visitor type definitions
const (
	RealName  VisitorType = "realname"  // Real-name user
	Anonymous VisitorType = "anonymous" // Anonymous user
	Business  VisitorType = "business"  // Application account
)

// ToAccessorType Converts to AccessorType
func (v VisitorType) ToAccessorType() AccessorType {
	switch v {
	case RealName:
		return AccessorTypeUser
	case Business:
		return AccessorTypeApp
	case Anonymous:
		return AccessorTypeAnonymous
	default:
		// Unknown visitor type, default to anonymous user
		return AccessorTypeAnonymous
	}
}

// AccessorType Accessor Type
type AccessorType string

const (
	AccessorTypeUser       AccessorType = "user"       // Real-name user
	AccessorTypeDepartment AccessorType = "department" // Department
	AccessorTypeGroup      AccessorType = "group"      // Organization
	AccessorTypeRole       AccessorType = "role"       // Role
	AccessorTypeApp        AccessorType = "app"        // Application account
	AccessorTypeAnonymous  AccessorType = "anonymous"  // Anonymous access
)

// ToVisitorType Converts AccessorType to VisitorType
func (a AccessorType) ToVisitorType() VisitorType {
	switch a {
	case AccessorTypeUser:
		return RealName
	case AccessorTypeApp:
		return Business
	case AccessorTypeAnonymous:
		return Anonymous
	case AccessorTypeDepartment, AccessorTypeGroup, AccessorTypeRole:
		return ""
	default:
		return ""
	}
}

// AccountType Login account type
type AccountType int32

// Login account type definition
const (
	Other  AccountType = 0
	IDCard AccountType = 1
)

const (
	// AccessedByUser Real-name user
	AccessedByUser string = "accessed_by_users"
	// AccessedByAnyOne Anonymous user
	AccessedByAnyOne string = "accessed_by_anyone"
)

// ClientType Device type
type ClientType int32

// ClientTypeMap Client type map
var ClientTypeMap = map[ClientType]string{
	Unknown:      "unknown",
	IOS:          "ios",
	Android:      "android",
	WindowsPhone: "windows_phone",
	Windows:      "windows",
	MacOS:        "mac_os",
	Web:          "web",
	MobileWeb:    "mobile_web",
	Nas:          "nas",
	ConsoleWeb:   "console_web",
	DeployWeb:    "deploy_web",
	Linux:        "linux",
	APP:          "app",
}

// ReverseClientTypeMap Reverse client type map
var ReverseClientTypeMap = map[string]ClientType{
	"unknown":       Unknown,
	"ios":           IOS,
	"android":       Android,
	"windows_phone": WindowsPhone,
	"windows":       Windows,
	"mac_os":        MacOS,
	"web":           Web,
	"mobile_web":    MobileWeb,
	"nas":           Nas,
	"console_web":   ConsoleWeb,
	"deploy_web":    DeployWeb,
	"linux":         Linux,
	"app":           APP,
}

// AccountTypeMap Account type map
var AccountTypeMap = map[AccountType]string{
	Other:  "other_category",
	IDCard: "id_card",
}

// ReverseAccountTypeMap Reverse account type map
var ReverseAccountTypeMap = map[string]AccountType{
	"other_category": Other,
	"id_card":        IDCard,
}

func (typ ClientType) String() string {
	str, ok := ClientTypeMap[typ]
	if !ok {
		str = ClientTypeMap[Unknown]
	}
	return str
}

// Device type definition
const (
	Unknown ClientType = iota
	IOS
	Android
	WindowsPhone
	Windows
	MacOS
	Web
	MobileWeb
	Nas
	ConsoleWeb
	DeployWeb
	Linux
	APP
)

// TokenInfo Authorization verification information
type TokenInfo struct {
	Active     bool        // Token status
	VisitorID  string      // Visitor ID
	Scope      string      // Permission scope
	ClientID   string      // Client ID
	VisitorTyp VisitorType // Visitor type
	// Following fields exist only when visitorType=realname (real-name user)
	LoginIP        string      // Login IP
	Udid           string      // Device ID
	AccountTyp     AccountType // Account type
	ClientTyp      ClientType  // Device type
	PhoneNumber    string      // Phone number for anonymous users
	VisitorName    string      // Nickname for anonymous visitors
	CredentialID   string      // Verified credential id when one exists (for example AppKey id)
	CredentialName string      // Verified credential display name when one exists
	MAC            string      // MAC address
	UserAgent      string      // User agent info
}

// Hydra Authorization service interface
type Hydra interface {
	Introspect(ctx context.Context, token string) (tokenInfo *TokenInfo, err error)
}

// AppKeyPrefix marks a user-issued AppKey (API Key) credential. The public-API
// auth middleware branches on this prefix: keys are verified by bkn-safe, all
// other bearer tokens by hydra introspection.
const AppKeyPrefix = "bak_"

// AppKeyVerifier resolves a user-issued AppKey to the owner's TokenInfo by asking
// bkn-safe. It mirrors Hydra.Introspect's contract (same TokenInfo result) so the
// gateway middleware can treat an AppKey and an OAuth token interchangeably.
type AppKeyVerifier interface {
	Verify(ctx context.Context, key string) (tokenInfo *TokenInfo, err error)
}

// KnDataSourceConfig Knowledge network data source configuration
type KnDataSourceConfig struct {
	KnowledgeNetworkID string `json:"knowledge_network_id"` // Knowledge Network ID
}

// ConceptRetrievalConfig concept recall configuration.
type ConceptRetrievalConfig struct {
	ConceptGroups          []string `json:"concept_groups,omitempty"`
	ObjectTypes            []string `json:"object_types,omitempty"`              // Recall only these object type ids.
	ExcludeObjectTypes     []string `json:"exclude_object_types,omitempty"`      // Drop these object type ids from recall.
	TopK                   int      `json:"top_k,omitempty"`                     // Default is 10.
	IncludeSampleData      bool     `json:"include_sample_data,omitempty"`       // Default is false.
	SchemaBrief            bool     `json:"schema_brief,omitempty"`              // Default is false.
	PerObjectPropertyTopK  int      `json:"per_object_property_top_k,omitempty"` // Default8.
	GlobalPropertyTopK     int      `json:"global_property_top_k,omitempty"`     // Default30.
	EnablePropertyBrief    bool     `json:"enable_property_brief,omitempty"`     // Default is true.
	EnableCoarseRecall     bool     `json:"enable_coarse_recall,omitempty"`      // Default is true, enabling coarse recall.
	CoarseObjectLimit      int      `json:"coarse_object_limit,omitempty"`       // Default2000.
	CoarseRelationLimit    int      `json:"coarse_relation_limit,omitempty"`     // Default300.
	CoarseMinRelationCount int      `json:"coarse_min_relation_count,omitempty"` // Default is 5000, the minimum number of relations that triggers rough recall.
}

// PropertyFilterConfig propertyfilterconfiguration.
type PropertyFilterConfig struct {
	MaxPropertiesPerInstance int  `json:"max_properties_per_instance,omitempty"` // Default20.
	MaxPropertyValueLength   int  `json:"max_property_value_length,omitempty"`   // Default500.
	EnablePropertyFilter     bool `json:"enable_property_filter,omitempty"`      // Default is true.
}

// SemanticInstanceRetrievalConfig semanticsinstanceretrieveconfiguration.
type SemanticInstanceRetrievalConfig struct {
	PerTypeInstanceLimit              int     `json:"per_type_instance_limit,omitempty"`                // Default is 5.
	InitialCandidateCount             int     `json:"initial_candidate_count,omitempty"`                // Default50.
	EnableGlobalFinalScoreRatioFilter bool    `json:"enable_global_final_score_ratio_filter,omitempty"` // Default is true.
	GlobalFinalScoreRatio             float64 `json:"global_final_score_ratio,omitempty"`               // Default0.25.
	MaxSemanticSubConditions          int     `json:"max_semantic_sub_conditions,omitempty"`            // Default is 10.
	SemanticFieldKeepRatio            float64 `json:"semantic_field_keep_ratio,omitempty"`              // Default0.2.
	SemanticFieldKeepMin              int     `json:"semantic_field_keep_min,omitempty"`                // Default is 5.
	SemanticFieldKeepMax              int     `json:"semantic_field_keep_max,omitempty"`                // Default15.
	SemanticFieldRerankBatchSize      int     `json:"semantic_field_rerank_batch_size,omitempty"`       // Default128.
	MinDirectRelevance                float64 `json:"min_direct_relevance,omitempty"`                   // Default0.3.
	ExactNameMatchScore               float64 `json:"exact_name_match_score,omitempty"`                 // Default0.85.
	InstanceRerankMode                string  `json:"instance_rerank_mode,omitempty"`                   // off (default) / on / shadow.
	MinRerankerScore                  float64 `json:"min_reranker_score,omitempty"`                     // 0 keeps the deployment's value.
	ObjectTypeConcurrency             int     `json:"object_type_concurrency,omitempty"`                // Default 6.
	MinObjectTypeScoreRatio           float64 `json:"min_object_type_score_ratio,omitempty"`            // 0 disables the object type pre-filter.
}

// RetrievalConfig retrieveconfiguration.
type RetrievalConfig struct {
	ConceptRetrieval          *ConceptRetrievalConfig          `json:"concept_retrieval,omitempty"`
	SemanticInstanceRetrieval *SemanticInstanceRetrievalConfig `json:"semantic_instance_retrieval,omitempty"`
	PropertyFilter            *PropertyFilterConfig            `json:"property_filter,omitempty"`
}

// KnSearchScope is the scope block kn_search accepts.
//
// Only concept_groups: the include_object_types / include_relation_types / include_action_types
// switches that SearchScopeConfig also carries were accepted here and then dropped on the floor --
// kn_search never filtered its response by category. Taking them out of the contract keeps the
// request struct honest; search_schema keeps its own SearchSchemaScope, where those switches work.
type KnSearchScope struct {
	ConceptGroups []string `json:"concept_groups,omitempty"`
}

// KnSearchReq kn_search request
type KnSearchReq struct {
	// Header Parameters
	XAccountID   string `header:"x-account-id"`
	XAccountType string `header:"x-account-type"`

	// Body Parameters - use any to avoid defining complex structures explicitly
	// Corresponds to the complete request structure of data-retrieval interface
	Query           string                `json:"query" validate:"required"`
	KnID            string                `json:"kn_id" validate:"required"`
	knIDs           []*KnDataSourceConfig // Internal use, converted from KnID, not exposed
	SearchScope     *KnSearchScope        `json:"search_scope,omitempty"`
	RetrievalConfig any                   `json:"retrieval_config,omitempty"`
	OnlySchema      *bool                 `json:"only_schema,omitempty"`
	EnableRerank    *bool                 `json:"enable_rerank,omitempty"`
	RerankModel     *string               `json:"rerank_model,omitempty"` // Finely sorted small model name coverage; use the default reranker of the model management configuration out of the box.
	IncludeColumns  *bool                 `json:"include_columns,omitempty"`
	// IndexOpsOnly keeps only index-derived operators in response condition_operations. It is set by the MCP layer,
	// Not entering the request contract: the comparison operator can be deduced according to the attribute type, and issuing it one by one is pure noise to the Agent; the REST caller.
	// Consumers with direct BKN connections (such as Studio) still get the full amount.
	IndexOpsOnly bool `json:"-"`
}

// SetKnIDs Sets knIDs (internal use, converted from KnID)
func (r *KnSearchReq) SetKnIDs(knIDs []*KnDataSourceConfig) {
	r.knIDs = knIDs
}

// GetKnIDs Gets knIDs (internal use)
func (r *KnSearchReq) GetKnIDs() []*KnDataSourceConfig {
	return r.knIDs
}

// KnSearchResp kn_search response
type KnSearchResp struct {
	// Use any to directly return the original structure from the underlying interface
	// Corresponds to the complete response structure of data-retrieval interface
	ObjectTypes   any     `json:"object_types,omitempty"`
	RelationTypes any     `json:"relation_types,omitempty"`
	ActionTypes   any     `json:"action_types,omitempty"`
	Nodes         any     `json:"nodes,omitempty"`
	Message       *string `json:"message,omitempty"`
}

// LLMMessage LLM conversation message.
type LLMMessage struct {
	Role    string `json:"role"`    // "system" | "user" | "assistant"
	Content string `json:"content"` // Message content.
}

// LLMChatReq LLM conversation request.
type LLMChatReq struct {
	Model            string       `json:"model"`                       // Model name.
	Messages         []LLMMessage `json:"messages"`                    // Conversation message list.
	Temperature      float64      `json:"temperature,omitempty"`       // Temperature parameters.
	TopK             int          `json:"top_k,omitempty"`             // TopKsampling.
	TopP             float64      `json:"top_p,omitempty"`             // TopPsampling.
	FrequencyPenalty float64      `json:"frequency_penalty,omitempty"` // Frequency penalty.
	PresencePenalty  float64      `json:"presence_penalty,omitempty"`  // Presence penalty.
	MaxTokens        int          `json:"max_tokens,omitempty"`        // Maximum token count.
	Stream           bool         `json:"stream,omitempty"`            // Whether to stream.
	AccountID        string       `json:"-"`                           // Account ID (for Header)
	AccountType      string       `json:"-"`                           // Account type (for Header)
}

// DrivenMFModelAPIClient MF-Model API client interface.
// Unifiedly provides LLM dialogue and vector reordering capabilities.
type DrivenMFModelAPIClient interface {
	// Chat conversation, return complete response content.
	Chat(ctx context.Context, req *LLMChatReq) (content string, err error)
	// Rerank reorders documents; when model is empty, the default reranker model is used.
	Rerank(ctx context.Context, query string, documents []string, model string) (*RerankResp, error)
}

// RerankResult single reranking result.
type RerankResult struct {
	Index          int     `json:"index"`           // Document index.
	RelevanceScore float64 `json:"relevance_score"` // relevance score.
	Document       *string `json:"document"`        // the original document (usually null)
}

// RerankResp Rerank response.
type RerankResp struct {
	Results []RerankResult `json:"results"` // Reorder results list.
}
