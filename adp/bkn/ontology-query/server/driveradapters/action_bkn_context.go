// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package driveradapters

import (
	"context"
	"fmt"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/ontology-query/server/interfaces"
)

// resolveActionBKNContext sets req.ConversationID and req.InteractionID, the correlation
// labels an action execution is stored with.
//
// Each id comes from the body's bkn_context, else from the bkn-conversation-id /
// bkn-interaction-id headers already parsed into the request's TraceContext, so the Context
// Loader path needs no change. A body value that is not a well-formed business trace id, a
// body value that differs from the header's, and a body interaction without its conversation
// are errors. An interaction id that comes only from the header, without a conversation id,
// is dropped rather than rejected. Both ids are labels the caller owns: nothing here creates, infers or checks them
// against the trace core, and nothing may use them for authorization.
//
// A scheduled execution, or one that handles an event, has no caller run, so both stay empty
// and are not validated.
func resolveActionBKNContext(ctx context.Context, req *interfaces.ActionExecutionRequest) error {
	req.ConversationID, req.InteractionID = "", ""
	if req.TriggerType == interfaces.TriggerTypeScheduled || req.TriggerType == interfaces.TriggerTypeEvent {
		return nil
	}

	var header common.TraceContext
	if tc, ok := common.GetTraceContextFromCtx(ctx); ok {
		header = tc
	}
	var body interfaces.ActionBKNContext
	if req.BKNContext != nil {
		body = *req.BKNContext
	}

	conversationID, err := mergeActionBKNID("conversation_id", body.ConversationID, header.ConversationID)
	if err != nil {
		return err
	}
	interactionID, err := mergeActionBKNID("interaction_id", body.InteractionID, header.InteractionID)
	if err != nil {
		return err
	}
	if interactionID != "" && conversationID == "" {
		if strings.TrimSpace(body.InteractionID) != "" {
			return fmt.Errorf("interaction_id requires conversation_id: an Interaction belongs to a Conversation")
		}
		// The interaction id came from the header alone. Callers sending only
		// bkn-interaction-id were accepted before the ids were recorded, so record none, the
		// same way an invalid header value reads as absent.
		interactionID = ""
	}
	req.ConversationID, req.InteractionID = conversationID, interactionID
	return nil
}

// mergeActionBKNID picks one id from the body and the header value. The header value was
// already validated when the trace context was built, so an invalid header reads as absent.
func mergeActionBKNID(field, bodyValue, headerValue string) (string, error) {
	bodyValue = strings.TrimSpace(bodyValue)
	if bodyValue != "" {
		if common.SanitizeBusinessTraceID(bodyValue) == "" {
			return "", fmt.Errorf("bkn_context.%s is not a valid id: use 1-128 characters of letters, digits, '.', '_', ':' or '-', starting with a letter or digit", field)
		}
		if headerValue != "" && headerValue != bodyValue {
			return "", fmt.Errorf("bkn_context.%s conflicts with the bkn-%s header", field, strings.ReplaceAll(field, "_", "-"))
		}
		return bodyValue, nil
	}
	return headerValue, nil
}
