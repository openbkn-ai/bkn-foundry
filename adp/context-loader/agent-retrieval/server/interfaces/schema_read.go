// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"net/url"
)

// SchemaReadAccess exposes only the complete-schema reads used by OSDK.
// The response stays raw: decoding through the agent-facing detail structs would
// discard display keys, metric dimensions, and future schema fields.
type SchemaReadAccess interface {
	ReadSchema(ctx context.Context, knID, collection string, query url.Values) (int, []byte, error)
}
