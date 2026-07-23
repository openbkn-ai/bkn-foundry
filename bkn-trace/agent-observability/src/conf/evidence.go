package conf

import "os"

type EvidenceConfig struct {
	Store string
}

func NewEvidenceConfig() EvidenceConfig {
	store := os.Getenv("BKN_TRACE_EVIDENCE_STORE")
	if store == "" {
		store = "memory"
	}
	return EvidenceConfig{Store: store}
}
