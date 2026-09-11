// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Package propertyaccess defines the stable access-level vocabulary shared by
// authorization decision points and the services that enforce their results.
package propertyaccess

import (
	"encoding/json"
	"errors"
	"fmt"
)

// ErrInvalidLevel is returned whenever an unknown access level reaches a
// contract boundary. Callers must fail closed instead of guessing a fallback.
var ErrInvalidLevel = errors.New("invalid property access level")

// Level is the effective visibility of one object-type data property.
//
// The declaration order is the security order. Keep rank as the only ordering
// implementation so comparisons, minimums, and maximums cannot drift apart.
type Level string

const (
	None   Level = "none"
	Schema Level = "schema"
	Masked Level = "masked"
	Full   Level = "full"
)

// Parse validates and returns one wire-format level.
func Parse(value string) (Level, error) {
	level := Level(value)
	if !level.Valid() {
		return "", fmt.Errorf("%w: %q", ErrInvalidLevel, value)
	}
	return level, nil
}

// Valid reports whether level belongs to the stable four-level vocabulary.
func (level Level) Valid() bool {
	switch level {
	case None, Schema, Masked, Full:
		return true
	default:
		return false
	}
}

// Compare returns -1, 0, or 1 when left is below, equal to, or above right.
// Unknown inputs return ErrInvalidLevel rather than being ordered as a zero
// value, which would silently turn a contract error into an authorization rule.
func Compare(left, right Level) (int, error) {
	leftRank, err := rank(left)
	if err != nil {
		return 0, err
	}
	rightRank, err := rank(right)
	if err != nil {
		return 0, err
	}
	switch {
	case leftRank < rightRank:
		return -1, nil
	case leftRank > rightRank:
		return 1, nil
	default:
		return 0, nil
	}
}

// Min returns the more restrictive of two valid levels.
func Min(left, right Level) (Level, error) {
	comparison, err := Compare(left, right)
	if err != nil {
		return "", err
	}
	if comparison <= 0 {
		return left, nil
	}
	return right, nil
}

// Max returns the less restrictive of two valid levels.
func Max(left, right Level) (Level, error) {
	comparison, err := Compare(left, right)
	if err != nil {
		return "", err
	}
	if comparison >= 0 {
		return left, nil
	}
	return right, nil
}

// MarshalJSON rejects unknown values so a producer cannot publish an invalid
// decision that a downstream consumer might interpret permissively.
func (level Level) MarshalJSON() ([]byte, error) {
	if !level.Valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidLevel, level)
	}
	return json.Marshal(string(level))
}

// UnmarshalJSON validates levels at the wire boundary.
func (level *Level) UnmarshalJSON(data []byte) error {
	if level == nil {
		return fmt.Errorf("%w: nil destination", ErrInvalidLevel)
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidLevel, err)
	}
	parsed, err := Parse(value)
	if err != nil {
		return err
	}
	*level = parsed
	return nil
}

func rank(level Level) (int, error) {
	switch level {
	case None:
		return 0, nil
	case Schema:
		return 1, nil
	case Masked:
		return 2, nil
	case Full:
		return 3, nil
	default:
		return 0, fmt.Errorf("%w: %q", ErrInvalidLevel, level)
	}
}
