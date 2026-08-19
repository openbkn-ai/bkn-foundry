package toolbox

import (
	"context"
	"net/http"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/telemetry"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/utils"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
)

// HandleOperatorDeleteEvent operator delete event.
func (s *ToolServiceImpl) HandleOperatorDeleteEvent(ctx context.Context, message []byte) error {
	// record observable.
	var err error
	ctx, _ = oteltrace.StartInternalSpan(ctx)
	defer oteltrace.EndSpan(ctx, err)
	telemetry.SetSpanAttributes(ctx, map[string]interface{}{
		"topic":   interfaces.OperatorDeleteEventTopic,
		"message": string(message),
	})
	s.Logger.WithContext(ctx).Debugf("handle operator delete event topic: %s, message: %s", interfaces.OperatorDeleteEventTopic, string(message))
	defer func() {
		if err != nil {
			s.Logger.WithContext(ctx).Debugf("handle operator delete event topic: %s, failed: message: %s, err: %v", interfaces.OperatorDeleteEventTopic, string(message), err)
		}
	}()
	// Parse the message. If the message format parsing fails, an error will be printed.
	operatorDeleteEvent := &interfaces.OperatorDeleteEvent{}
	err = utils.StringToObject(string(message), operatorDeleteEvent)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("parse operator delete event failed, message: %s, err: %v", string(message), err)
		return nil
	}

	// 1. Query tool information based on OperatorID.
	toolDBs, err := s.ToolDB.SelectToolBySource(ctx, model.SourceTypeOperator, operatorDeleteEvent.OperatorID)
	if err != nil {
		s.Logger.WithContext(ctx).Errorf("select tool by source failed, err: %v", err)
		return err
	}
	// Tools that do not rely on changing operators do not need to be processed.
	if len(toolDBs) == 0 {
		return nil
	}
	// 2. Delete tools based on tool information.
	for _, toolDB := range toolDBs {
		// If the tool status is disabled, skip it directly.
		if toolDB.Status == interfaces.ToolStatusTypeDisabled.String() {
			continue
		}
		// Disable the tool. If it fails, it will directly return an error and wait for the message to be re-delivered.
		err = s.ToolDB.UpdateToolStatus(ctx, nil, toolDB.ToolID, interfaces.ToolStatusTypeDisabled.String(), operatorDeleteEvent.UpdateUser)
		if err != nil {
			s.Logger.WithContext(ctx).Errorf("update tool status failed, err: %v", err)
			err = errors.DefaultHTTPError(ctx, http.StatusInternalServerError, err.Error())
			return err
		}
	}
	return nil
}
