// Copyright openbkn.ai
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.
package kbrefresh

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	kb "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/extension/knowledgebase"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/kbinstance"
	"strings"
	"sync/atomic"
	"time"
)

// ProcessLease is shared by this process's authenticated command handlers and
// its worker. Compare-and-set intents still serialize concurrent submissions;
// another replica must acquire the durable lineage lease before continuing.
var ProcessLease = uuid.NewString()
var configuredLimits atomic.Pointer[Limits]

// ConfigureLimits installs the same immutable startup settings for command
// handlers and the worker. Restarting applies a changed deployment setting.
func ConfigureLimits(limits *Limits) {
	if limits == nil {
		configuredLimits.Store(nil)
		return
	}
	copy := *limits
	configuredLimits.Store(&copy)
}

func NewService(limits *Limits) *Service {
	if limits == nil {
		limits = configuredLimits.Load()
	}
	service := &Service{Git: logics.GS, Limits: limits, Journal: logics.KBRefreshes, Releases: logics.RA, Publications: logics.KBPublications, Builds: logics.KBBA, Vega: logics.VA}
	service.Enabled = func() bool { _, enabled := kb.Lookup(); return enabled }
	service.Network, _ = logics.BBA.(kb.PublishedModelCheck)
	if providers, enabled := kb.Lookup(); enabled {
		service.Sources = providers.RefreshSources
		service.Tasks = providers.RefreshTasks
		service.Storage, _ = providers.Datasets.(kb.PublicationDatasets)
	}
	return service
}

// RunWorker discovers ready publications, but only follows the explicit current
// consumer switch. Status GETs never create work. Licences and source/model
// guards are rechecked within each action, not just at process startup.
func RunWorker(ctx context.Context, limits *Limits) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	after := ""
	log := kbinstance.NewWorkerLog("refresh")
	observed := map[string]string{}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		service := NewService(limits)
		_, access := kb.ForWorker()
		if access == kb.WorkerNotInstalled {
			continue
		}
		paused := ""
		if access == kb.WorkerPause {
			paused = "licence not known yet; refresh paused"
		}
		log.Condition("licence", paused, "licence known; refresh resumed")
		if access != kb.WorkerRun {
			continue
		}
		if service.Journal == nil || service.Sources == nil || service.Tasks == nil || service.Storage == nil {
			log.Problem("configuration", errors.New("refresh sources, tasks or dataset storage are not installed"))
			continue
		}
		log.Problem("configuration", nil)
		discovery, ok := service.Publications.(interfaces.KBPublicationDiscovery)
		if !ok {
			continue
		}
		networks, err := discovery.ActiveNetworks(ctx, after, 16)
		log.Problem("discovery", err)
		if err != nil {
			continue
		}
		if len(networks) == 0 {
			after = ""
			continue
		}
		for _, network := range networks {
			after = network
			if ctx.Err() != nil {
				return
			}
			stepCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
			err := service.runNetwork(stepCtx, network, ProcessLease)
			cancel()
			if errors.Is(err, ErrLostLease) {
				err = nil // Another replica owns this lineage now; that is not a failure.
			}
			log.Result(network, err)
			log.Transition(network, service.observe(ctx, network, observed))
		}
	}
}

// observe reports a refresh's committed state change since the previous reading.
func (s *Service) observe(ctx context.Context, network string, observed map[string]string) []string {
	publication, err := s.Publications.Get(ctx, network)
	if err != nil || publication == nil {
		return nil
	}
	record, err := s.Journal.Get(ctx, publication.LineageID)
	if err != nil || record == nil {
		return nil
	}
	state := strings.TrimSpace(record.State + " " + record.Reason)
	previous, seen := observed[publication.LineageID]
	observed[publication.LineageID] = state
	if !seen || previous == state {
		return nil
	}
	return []string{fmt.Sprintf("%s -> %s", previous, state)}
}
func (s *Service) runNetwork(ctx context.Context, network, lease string) error {
	publication, err := s.Publications.Get(ctx, network)
	if err != nil || publication == nil {
		return err
	}
	current, err := Resolve(ctx, publication.LineageID, s.Releases, s.Publications)
	if err != nil {
		return err
	}
	if current.NetworkID != network {
		return nil
	}
	record, err := s.Journal.Get(ctx, publication.LineageID)
	if err != nil {
		return err
	}
	if record == nil || record.Pending == nil {
		record, err = s.Begin(ctx, publication.LineageID, uuid.NewString(), lease)
		if err != nil || record == nil {
			return err
		}
	}
	if record.State == "idle" && record.Pending == nil {
		return s.Reconcile(ctx, record, lease)
	}
	switch record.State {
	case "running":
		return s.ExtractStep(ctx, publication.LineageID, lease)
	case "ready":
		plan, err := s.application(ctx, record)
		if err != nil {
			if err == ErrLostLease {
				return err
			}
			return s.attention(ctx, record, "write_plan_unavailable")
		}
		return s.BeginApply(ctx, publication.LineageID, lease, plan.staged.Digest, plan.fusion.Preview.Signature)
	case "discarding":
		return s.DiscardStep(ctx, publication.LineageID, lease)
	case "applying", "finishing":
		return s.ApplyStep(ctx, publication.LineageID, lease)
	}
	return nil
}
