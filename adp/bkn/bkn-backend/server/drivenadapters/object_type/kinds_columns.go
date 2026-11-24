// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package object_type

import (
	"github.com/bytedance/sonic"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/common"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

// encodeKinds stores an object type's kinds as a JSON array, empty for a plain object type, and
// the event kind's configuration as JSON, NULL without it.
func encodeKinds(objectType *interfaces.ObjectType) (string, any, error) {
	kinds := ""
	if len(objectType.Kinds) > 0 {
		encoded, err := sonic.Marshal(objectType.Kinds)
		if err != nil {
			return "", nil, err
		}
		kinds = string(encoded)
	}
	var event any
	if objectType.Event != nil {
		encoded, err := sonic.Marshal(objectType.Event)
		if err != nil {
			return "", nil, err
		}
		event = string(encoded)
	}
	return kinds, event, nil
}

// decodeKinds reads what encodeKinds stored. A value that cannot be read is logged and left
// out: the object type is then read as a plain one.
func decodeKinds(kinds, event []byte) ([]string, *interfaces.ObjectTypeEvent) {
	var decodedKinds []string
	if len(kinds) > 0 {
		if err := common.UnmarshalStoredJSON(kinds, &decodedKinds); err != nil {
			logger.Errorf("decode the stored kinds of an object type: %v", common.SafeErrorSummary(err))
			return nil, nil
		}
	}
	var decodedEvent *interfaces.ObjectTypeEvent
	if len(event) > 0 {
		if err := common.UnmarshalStoredJSON(event, &decodedEvent); err != nil {
			logger.Errorf("decode the stored event configuration of an object type: %v", common.SafeErrorSummary(err))
			decodedEvent = nil
		}
	}
	return decodedKinds, decodedEvent
}
