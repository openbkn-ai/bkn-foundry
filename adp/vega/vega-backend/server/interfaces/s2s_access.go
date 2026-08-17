// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import "context"

// The s2sInternalAccessKey indicates that the current request comes from an internal S2S call within the cluster (/in/ internal network endpoint).
// The internal infrastructure resources of the system (internal_resource, such as the BKN concept dataset) are being used
// When internal services access on behalf of users, per-account authentication is allowed by default - such resources are never authorized to business users.
// Performing a per-account view_detail check on it will only result in a false rejection. Only the /in/ internal network processor sets this flag.
// The external network /v1 endpoint is not affected.
type s2sInternalAccessKey struct{}

// WithS2SInternalAccess is marked as internal S2S access on ctx. It is called only by the /in/ internal network processor.
func WithS2SInternalAccess(ctx context.Context) context.Context {
	return context.WithValue(ctx, s2sInternalAccessKey{}, true)
}

// IsS2SInternalAccess determines whether ctx is marked as internal S2S access.
func IsS2SInternalAccess(ctx context.Context) bool {
	v, ok := ctx.Value(s2sInternalAccessKey{}).(bool)
	return ok && v
}
