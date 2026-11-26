// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knevent

import (
	"context"
	"time"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
)

const (
	// ProjectionWorker is the name the worker that keeps the event classes'
	// datasets registers under.
	ProjectionWorker = "kn-event-classes"

	// projectPeriod is how often the feeds are looked at, which is about how
	// far the datasets are behind the ledger.
	projectPeriod = 5 * time.Second
	// projectRun bounds one round; what is left goes on the next.
	projectRun = time.Minute
	// retryAfter is how long a network or a class that failed is left alone.
	retryAfter = time.Minute
	// projectLock lets one replica write the datasets at a time, so an event
	// is never written older than it was written before.
	projectLock = "bkn_kn_event_projection"

	feedPage         = interfaces.KnEventChangeDefaultSize
	rewritePage      = 500
	networksPerRound = 500
	classesPerRound  = 500
)

// projector keeps the copy of the ledger in the event classes' datasets. It
// does two things in turn, both by one goroutine:
//
//   - writes the ledger's events of a class whose dataset lacks them: the
//     dataset was made after the class had events, or was lost;
//   - follows each network's feed, and writes every event that changed as it
//     is now.
//
// An event is only ever written as the ledger has it at the time, and after
// anything written of it before, so the datasets end as the ledger is.
type projector struct {
	store   *store
	classes *classes
	now     func() time.Time

	// resume is the last event written of a class that is being written from
	// the ledger, by network and class: a round goes on where the last stopped.
	resume map[string]string
	// retry is when what failed is tried again, by network, or network and class.
	retry map[string]time.Time
}

func newProjector(store *store, classes *classes) *projector {
	return &projector{
		store: store, classes: classes, now: time.Now,
		resume: map[string]string{}, retry: map[string]time.Time{},
	}
}

// run works every period until ctx ends.
func (p *projector) run(ctx context.Context) error {
	ticker := time.NewTicker(projectPeriod)
	defer ticker.Stop()
	for {
		p.round(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// round does one turn of the work, unless another replica is at it.
func (p *projector) round(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, projectRun)
	defer cancel()
	_, err := p.store.alone(ctx, projectLock, func() error {
		for _, step := range []struct {
			name string
			do   func(context.Context) error
		}{{"write ledger events", p.rewrite}, {"follow feeds", p.follow}} {
			if err := step.do(ctx); err != nil && ctx.Err() == nil {
				logger.Errorf("knowledge-network event classes: %s: %v", step.name, err)
			}
		}
		return nil
	})
	if err != nil && ctx.Err() == nil {
		logger.Errorf("knowledge-network event classes: %v", err)
	}
}

func classKey(knID, classID string) string { return knID + "\x00" + classID }

// waiting reports whether what failed under key is still left alone.
func (p *projector) waiting(key string) bool {
	until, failed := p.retry[key]
	if failed && !p.now().Before(until) {
		delete(p.retry, key)
		failed = false
	}
	return failed
}

func (p *projector) failed(key, what string, err error) {
	p.retry[key] = p.now().Add(retryAfter)
	logger.Errorf("knowledge-network event classes: %s: %v", what, err)
}

// rewrite writes the ledger's events of the classes whose datasets lack them.
func (p *projector) rewrite(ctx context.Context) error {
	classes, err := p.store.unsyncedClasses(ctx, classesPerRound)
	if err != nil {
		return err
	}
	for _, class := range classes {
		if ctx.Err() != nil {
			return nil
		}
		key := classKey(class.knID, class.id)
		if p.waiting(key) {
			continue
		}
		if err := p.rewriteClass(ctx, class, key); err != nil {
			p.failed(key, "write the events of class "+class.id+" of network "+class.knID, err)
		}
	}
	return nil
}

// rewriteClass writes a class's events page by page in the order of their IDs.
// An event that changes meanwhile is in the feed, which is followed after.
func (p *projector) rewriteClass(ctx context.Context, class classRef, key string) error {
	for {
		events, err := p.store.eventsOfClass(ctx, class.knID, class.id, p.resume[key], rewritePage)
		if err != nil {
			return err
		}
		if len(events) > 0 {
			restored, err := p.put(ctx, class.knID, class.id, events)
			if err != nil || restored {
				// A dataset made again holds nothing: the next round starts over.
				return err
			}
			p.resume[key] = events[len(events)-1].ID
		}
		if len(events) < rewritePage {
			break
		}
		if ctx.Err() != nil {
			return nil
		}
	}
	delete(p.resume, key)
	return p.store.setSynced(ctx, class.knID, class.id, true)
}

// put writes events to the dataset of their class. A dataset that is gone is
// made again, and its class marked for the ledger's events to be written to
// it: restored then says the events given were not written.
func (p *projector) put(ctx context.Context, knID, classID string, events []*interfaces.KnEvent) (restored bool, err error) {
	if err = p.classes.write(ctx, classDatasetID(knID, classID), events); err == nil {
		return false, nil
	}
	if ctx.Err() != nil {
		return false, err
	}
	made, restoreErr := p.classes.restore(ctx, knID, classID)
	if restoreErr != nil || !made {
		return false, err
	}
	logger.Warnf("knowledge-network event classes: dataset of class %s of network %s was gone and is made again",
		classID, knID)
	delete(p.resume, classKey(knID, classID))
	return true, p.store.setSynced(ctx, knID, classID, false)
}

// follow writes what changed in the networks whose feeds are ahead of their
// datasets.
func (p *projector) follow(ctx context.Context) error {
	positions, err := p.store.behind(ctx, networksPerRound)
	if err != nil {
		return err
	}
	for _, position := range positions {
		if ctx.Err() != nil {
			return nil
		}
		if p.waiting(position.knID) {
			continue
		}
		if err := p.followNetwork(ctx, position); err != nil {
			p.failed(position.knID, "follow the feed of network "+position.knID, err)
		}
	}
	return nil
}

// followNetwork reads a network's feed from where its datasets have got to
// and writes each event that changed, once, as it is now. The position moves
// on only past what was written. An event of a class the module does not keep
// a dataset for is passed over. So is a check's refresh: it changes nothing a
// dataset keeps.
func (p *projector) followNetwork(ctx context.Context, position feedPosition) error {
	after := position.projected
	for ctx.Err() == nil {
		changes, err := p.store.listChanges(ctx, position.knID, after, feedPage)
		if err != nil {
			return err
		}
		if len(changes) == 0 {
			// The rest of the feed, up to where it ended when the round began,
			// was refreshes that have expired and been purged.
			return p.store.setProjected(ctx, position.knID, position.last)
		}
		kept, err := p.store.classesOf(ctx, position.knID)
		if err != nil {
			return err
		}
		byClass := map[string][]*interfaces.KnEvent{}
		seen := make(map[string]bool, len(changes))
		for index := range changes {
			event := &changes[index].Event
			if changes[index].Action == interfaces.KnEventChecked || seen[event.ID] || !kept[event.EventType] {
				continue
			}
			seen[event.ID] = true
			byClass[event.EventType] = append(byClass[event.EventType], event)
		}
		for classID, events := range byClass {
			if _, err := p.put(ctx, position.knID, classID, events); err != nil {
				return err
			}
		}
		after = changes[len(changes)-1].Seq
		if err := p.store.setProjected(ctx, position.knID, after); err != nil {
			return err
		}
		if len(changes) < feedPage {
			return nil
		}
	}
	return nil
}
