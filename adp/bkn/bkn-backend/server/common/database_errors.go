// Copyright openbkn.ai

package common

import (
	"errors"
	"strings"
)

type databaseErrorFields interface {
	Get(byte) string
}

// DatabaseUniqueConstraint identifies duplicate-key errors without coupling the
// service layer to a concrete SQL driver. MySQL exposes error 1062 in Error(),
// while the Kingbase driver exposes PostgreSQL-compatible fields through Get.
func DatabaseUniqueConstraint(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var fields databaseErrorFields
	if errors.As(err, &fields) && fields.Get('C') == "23505" {
		return strings.ToLower(fields.Get('n')), true
	}

	message := err.Error()
	lower := strings.ToLower(message)
	if !strings.Contains(lower, "error 1062") && !strings.Contains(lower, "duplicate entry") {
		return "", false
	}
	for _, quote := range []byte{'\'', '`', '"'} {
		marker := "for key " + string(quote)
		start := strings.Index(lower, marker)
		if start < 0 {
			continue
		}
		start += len(marker)
		end := strings.IndexByte(lower[start:], quote)
		if end < 0 {
			continue
		}
		constraint := lower[start : start+end]
		if dot := strings.LastIndexByte(constraint, '.'); dot >= 0 {
			constraint = constraint[dot+1:]
		}
		return constraint, true
	}
	return "", true
}
