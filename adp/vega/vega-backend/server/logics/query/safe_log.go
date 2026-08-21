package query

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/bytedance/sonic"
)

func SafeQuerySummary(query any) string {
	var queryBytes []byte
	queryType := "unknown"
	switch value := query.(type) {
	case string:
		queryType = "sql"
		queryBytes = []byte(value)
	default:
		queryType = "structured"
		queryBytes, _ = sonic.Marshal(value)
	}
	hash := sha256.Sum256(queryBytes)
	return fmt.Sprintf("query_type=%s query_hash=%s query_length=%d", queryType, hex.EncodeToString(hash[:]), len(queryBytes))
}
