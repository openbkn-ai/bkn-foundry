package ievidenceledger

import (
	"context"
	"errors"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/valueobject/ledgervo"
)

var (
	ErrPayloadConflict   = errors.New("event payload conflicts with durable ledger")
	ErrSequenceConflict  = errors.New("producer sequence conflicts with durable ledger")
	ErrCausalityConflict = errors.New("event causality conflicts with durable ledger")
)

type Store interface {
	Commit(ctx context.Context, event ledgervo.Event) (ledgervo.DurableAck, error)
}
