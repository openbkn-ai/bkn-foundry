// Copyright openbkn.ai
// Copyright The kweaver.ai Authors.
//
// Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the project root for details.

package action_scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openbkn-ai/bkn-comm-go/logger"

	"ontology-query/interfaces"
)

// Environment variable for duplicate execution window (seconds).
// Set to 0 or a negative value to disable duplicate detection.
const (
	envDuplicateWindowSeconds     = "ACTION_EXECUTION_DUPLICATE_WINDOW_SECONDS"
	defaultDuplicateWindowSeconds = 60
)

// duplicateWindowSeconds is the lookback window for in-flight duplicate detection.
var duplicateWindowSeconds = defaultDuplicateWindowSeconds

func init() {
	if val := os.Getenv(envDuplicateWindowSeconds); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			duplicateWindowSeconds = n
			logger.Infof("Action execution duplicate window set to %d seconds", duplicateWindowSeconds)
		}
	}
}

// computeInstanceIdentityHash returns a stable SHA-256 hex digest of the resolved instance set.
// Identity map keys are ordered by encoding/json; instance digests are sorted so request order does not matter.
func computeInstanceIdentityHash(instances []interfaces.ObjectSystemInfo) (string, error) {
	if len(instances) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(instances))
	for _, inst := range instances {
		b, err := json.Marshal(inst.InstanceIdentity)
		if err != nil {
			return "", fmt.Errorf("marshal instance identity: %w", err)
		}
		parts = append(parts, string(b))
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

// defaultDuplicateCheck rejects when the same kn + action type + instance fingerprint
// already has a pending/running execution started within the configured window.
// Returns (true, nil) to proceed, (false, nil) to reject as duplicate.
func (s *actionSchedulerService) defaultDuplicateCheck(ctx context.Context, req *interfaces.ActionExecutionRequest) (bool, error) {
	if duplicateWindowSeconds <= 0 {
		return true, nil
	}
	hash := req.InstanceIdentityHash
	if hash == "" {
		var err error
		hash, err = computeInstanceIdentityHash(req.Instances)
		if err != nil {
			return false, fmt.Errorf("compute instance identity hash: %w", err)
		}
		if hash == "" {
			return true, nil
		}
		req.InstanceIdentityHash = hash
	}

	now := time.Now().UnixMilli()
	from := now - int64(duplicateWindowSeconds)*1000
	list, err := s.logsService.QueryExecutions(ctx, &interfaces.ActionLogQuery{
		KNID:                 req.KNID,
		ActionTypeID:         req.ActionTypeID,
		Statuses:             []string{interfaces.ExecutionStatusPending, interfaces.ExecutionStatusRunning},
		InstanceIdentityHash: hash,
		StartTimeRange:       []int64{from, now},
		Limit:                1,
	})
	if err != nil {
		return false, fmt.Errorf("query in-flight executions for duplicate check: %w", err)
	}
	if list != nil && len(list.Entries) > 0 {
		logger.Infof(
			"Duplicate action execution detected: kn_id=%s action_type_id=%s hash=%s existing=%s",
			req.KNID, req.ActionTypeID, hash, list.Entries[0].ID,
		)
		return false, nil
	}
	return true, nil
}
