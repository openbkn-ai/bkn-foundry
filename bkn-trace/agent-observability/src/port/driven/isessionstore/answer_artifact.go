// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package isessionstore

import "errors"

var ErrAnswerArtifactTooLarge = errors.New("answer artifact exceeds durable storage packet budget")

// AnswerArtifactTooLargeError describes the encoded durable task budget, not
// the public artifact content limit. It is returned only inside the authorized
// terminal transaction and carries no artifact content.
type AnswerArtifactTooLargeError struct {
	PayloadBytes   uint64
	RequiredBytes  uint64
	MaxPacketBytes uint64
}

func (e *AnswerArtifactTooLargeError) Error() string { return ErrAnswerArtifactTooLarge.Error() }
func (e *AnswerArtifactTooLargeError) Unwrap() error { return ErrAnswerArtifactTooLarge }
