package safelog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

func SQLSummary(sql string, args []any) string {
	hash := sha256.Sum256([]byte(sql))
	return fmt.Sprintf("query_hash=%s query_length=%d args_count=%d", hex.EncodeToString(hash[:]), len(sql), len(args))
}
