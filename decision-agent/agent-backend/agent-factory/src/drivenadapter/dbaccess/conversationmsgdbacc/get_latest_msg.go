package conversationmsgdbacc

import (
	"context"

	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/common/chelper/dbhelper2"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/otel/oteltrace"
	"github.com/openbkn-ai/bkn-foundry/decision-agent/agent-backend/agent-factory/src/infra/persistence/dapo"
)

func (r *ConversationMsgRepo) GetLatestMsgByConversationID(ctx context.Context, conversationID string) (po *dapo.ConversationMsgPO, err error) {
	ctx, span := oteltrace.StartInternalSpan(ctx)
	defer span.End()
	oteltrace.SetConversationID(ctx, conversationID)

	po = &dapo.ConversationMsgPO{}
	sr := dbhelper2.NewSQLRunner(r.db, r.logger)
	sr.FromPo(po)
	sr.WhereEqual("f_conversation_id", conversationID)
	sr.Order("f_index DESC")
	sr.Limit(1)

	err = sr.FindOne(po)
	if err != nil {
		return nil, err
	}

	return po, nil
}
