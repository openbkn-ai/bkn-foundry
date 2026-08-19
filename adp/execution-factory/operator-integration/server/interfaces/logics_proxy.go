package interfaces

import (
	"context"
	"io"
)

// ProxyHandler proxy handler.
//
//go:generate mockgen -source=logics_proxy.go -destination=../mocks/logics_proxy.go -package=mocks
type ProxyHandler interface {
	HandlerRequest(ctx context.Context, req *HTTPRequest) (resp *HTTPResponse, err error)
}

// IOutboxMessageEvent message event management.
type IOutboxMessageEvent interface {
	Publish(ctx context.Context, req *OutboxMessageReq) (err error)
}

// Forwarder forwarder interface.
type Forwarder interface {
	Forward(ctx context.Context, req *HTTPRequest) (*HTTPResponse, error)
	ForwardStream(ctx context.Context, req *HTTPRequest) (*HTTPResponse, error)
}

// StreamProcessor stream processor interface.
type StreamProcessor interface {
	ProcessSSE(ctx context.Context, reader io.Reader, writer io.Writer) error
	ProcessHTTPStream(ctx context.Context, reader io.Reader, writer io.Writer) error
}

// FunctionProxyExecuteCodeReq function proxy execution code request.
type FunctionProxyExecuteCodeReq struct {
	Code            string            `json:"code" validate:"required"`                                      // Execute code.
	Event           map[string]any    `json:"event" validate:"required"`                                     // event.
	Language        string            `json:"language" default:"python"`                                     // execution language.
	Timeout         int               `json:"timeout,omitempty"`                                             // Timeout time in seconds.
	Source          string            `json:"source,omitempty"`                                              // execution source.
	TaskID          string            `json:"task_id,omitempty"`                                             // Task ID.
	CapabilityID    string            `json:"capability_id,omitempty"`                                       // Capability ID.
	CapabilityName  string            `json:"capability_name,omitempty"`                                     // Ability name.
	UserID          string            `json:"user_id,omitempty"`                                             // User ID.
	UserName        string            `json:"user_name,omitempty"`                                           // Username.
	Dependencies    []*DependencyInfo `json:"dependencies,omitempty"`                                        // Depend on resources.
	DependenciesURL string            `json:"dependencies_url,omitempty" default:"https://pypi.org/simple/"` // Installation source URL.
	// The following three items are used by sandbox_sdk.bkn in the sandbox to call BKN and converted into process-level environment variables for this execution.
	//
	// Use environment variables instead of events: event is the business input parameter of the user function, and mixing credentials in it will pollute its.
	// The parameter namespace also allows everyone who wants to call BKN to know what to put in the event. After going env.
	// They cannot be seen in the user code. They are the same existing channel as the task_id / user_id tracking fields.
	//
	// The lifecycle is the same as event: executor creates an environment for each execution and then --setenv enters bwrap.
	// Die with the process. Note the difference from env_vars when creating a session, which is container-level and will survive across callers.
	BKNToken          string `json:"bkn_token,omitempty"`           // The caller token by which the sandbox accesses the BKN as the caller.
	BKNConversationID string `json:"bkn_conversation_id,omitempty"` // Session ID, taken from bkn_start_interaction.
	BKNInteractionID  string `json:"bkn_interaction_id,omitempty"`  // Interaction ID, same as above.
}
