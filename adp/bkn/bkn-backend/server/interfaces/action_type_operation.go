// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"database/sql"
)

// ActionTypeOperation is an operation category of action types that a module provides, such as
// event, the recognition action of an event class, which the event module provides. The action
// type service hands a save of such an action type to it.
type ActionTypeOperation interface {
	// Available reports whether the license allows action types of the category to be created
	// or changed, and run. An unavailable category is refused as an unknown one.
	Available() bool
	// Prepare checks the action type against its network, in the transaction of the save when
	// there is one, and fills what the platform keeps of it. batch holds the definitions saved
	// with it, or is nil.
	Prepare(ctx context.Context, tx *sql.Tx, actionType *ActionType, batch *BatchIDIndex) error
	// MainChanged runs after the main branch of the network changed: a save without versions, a
	// publish, or a switch of the running version.
	MainChanged(ctx context.Context, knID string)
}
