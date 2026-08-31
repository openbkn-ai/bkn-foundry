// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package observabilityvo

import "context"

type sourceAuthorizationContextKey struct{}

func WithSourceAuthorization(ctx context.Context, authorization string) context.Context {
	return context.WithValue(ctx, sourceAuthorizationContextKey{}, authorization)
}

func SourceAuthorization(ctx context.Context) string {
	value, _ := ctx.Value(sourceAuthorizationContextKey{}).(string)
	return value
}
