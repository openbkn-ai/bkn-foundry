// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt.

package boot

import (
	"context"
	"log"
	"time"

	"github.com/openbkn-ai/bkn-foundry/bkn-trace/agent-observability/src/domain/service/capturecontrollersvc"
)

func runCapturePolicyController(ctx context.Context, controller *capturecontrollersvc.Controller) {
	if controller == nil {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		_, err := controller.Reconcile(ctx)
		if err != nil && ctx.Err() == nil {
			log.Printf("capture policy controller reconcile failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
