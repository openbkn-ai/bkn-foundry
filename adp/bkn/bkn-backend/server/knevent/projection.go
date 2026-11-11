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
	// backfillPeriod is how often event types without a class are looked for.
	// There are only those created before events were object types, and
	// those whose class could not be made.
	backfillPeriod = 10 * time.Minute
	// retryAfter is how long a network or an event type that failed is left alone.
	retryAfter = time.Minute
	// projectLock lets one replica write the datasets at a time, so an event
	// is never written older than it was written before.
	projectLock = "bkn_kn_event_projection"

	feedPage         = interfaces.KnEventChangeDefaultSize
	rewritePage      = 500
	networksPerRound = 500
	typesPerRound    = 500
)

// projector keeps the copy of the ledger in the event classes' datasets. It
// does three things in turn, all by one goroutine:
//
//   - gives a class to the event types that have none;
//   - writes the ledger's events of a type whose dataset lacks them: the type
//     was just given the dataset, or the dataset was lost;
//   - follows each network's feed, and writes every event that changed as it
//     is now.
//
// An event is only ever written as the ledger has it at the time, and after
// anything written of it before, so the datasets end as the ledger is.
type projector struct {
	store   *store
	classes *classes
	// allowed reports whether the licence covers events.
	allowed func() bool
	now     func() time.Time

	backfilled time.Time
	// resume is the last event written of a type that is being written from
	// the ledger, by network and code: a round goes on where the last stopped.
	resume map[string]string
	// retry is when what failed is tried again, by network, or network and code.
	retry map[string]time.Time
}

func newProjector(store *store, classes *classes, allowed func() bool) *projector {
	return &projector{
		store: store, classes: classes, allowed: allowed, now: time.Now,
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
	if !p.allowed() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, projectRun)
	defer cancel()
	_, err := p.store.alone(ctx, projectLock, func() error {
		for _, step := range []struct {
			name string
			do   func(context.Context) error
		}{{"give classes", p.backfill}, {"write ledger events", p.rewrite}, {"follow feeds", p.follow}} {
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

func typeKey(knID, code string) string { return knID + "\x00" + code }

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

// backfill gives a class to the event types that have none. A versioned
// network is left out: its class would be written to its draft, which the
// people working on the draft have not asked for. Such a type gets its class
// when it is next updated.
func (p *projector) backfill(ctx context.Context) error {
	if p.now().Sub(p.backfilled) < backfillPeriod {
		return nil
	}
	types, err := p.store.classlessTypes(ctx, typesPerRound)
	if err != nil {
		return err
	}
	p.backfilled = p.now()
	versioned := map[string]bool{}
	for _, eventType := range types {
		if ctx.Err() != nil {
			return nil
		}
		waits, known := versioned[eventType.KNID]
		if !known {
			if waits, err = p.classes.versioned(ctx, eventType.KNID); err != nil {
				logger.Warnf("knowledge-network event classes: read the version state of network %s: %v", eventType.KNID, err)
				continue
			}
			versioned[eventType.KNID] = waits
		}
		if waits {
			continue
		}
		if err := p.give(ctx, eventType); err != nil {
			logger.Warnf("knowledge-network event classes: give event type %s of network %s its class: %v",
				eventType.Code, eventType.KNID, err)
		}
	}
	return nil
}

// give makes the class of an event type and stores it with the type, unless
// the type was given one or deleted meanwhile, or another type has the class.
func (p *projector) give(ctx context.Context, listed interfaces.KnEventType) error {
	return p.store.exclusive(ctx, classLock(listed.KNID), func() error {
		eventType, found, err := p.store.getType(ctx, listed.KNID, listed.Code)
		if err != nil || !found || eventType.ObjectTypeID != "" {
			return err
		}
		if err := claim(ctx, p.store, eventType.KNID, eventType.Code, false); err != nil {
			return err
		}
		class, err := p.classes.ensure(ctx, eventType)
		if err != nil {
			return err
		}
		return p.store.write(ctx, eventType.KNID, func(w *writer) error {
			current, found, err := w.eventType(eventType.Code)
			if err != nil || !found || current.ObjectTypeID != "" {
				return err
			}
			if err := p.classes.clear(ctx, eventType, class); err != nil {
				return err
			}
			return w.setClass(eventType.Code, class)
		})
	})
}

// rewrite writes the ledger's events of the types whose datasets lack them.
func (p *projector) rewrite(ctx context.Context) error {
	types, err := p.store.unsyncedTypes(ctx, typesPerRound)
	if err != nil {
		return err
	}
	for _, eventType := range types {
		if ctx.Err() != nil {
			return nil
		}
		key := typeKey(eventType.KNID, eventType.Code)
		if p.waiting(key) {
			continue
		}
		if err := p.rewriteType(ctx, eventType, key); err != nil {
			p.failed(key, "write the events of type "+eventType.Code+" of network "+eventType.KNID, err)
		}
	}
	return nil
}

// rewriteType writes a type's events page by page in the order of their IDs.
// An event that changes meanwhile is in the feed, which is followed after.
func (p *projector) rewriteType(ctx context.Context, eventType interfaces.KnEventType, key string) error {
	for {
		events, err := p.store.eventsOfType(ctx, eventType.KNID, eventType.Code, p.resume[key], rewritePage)
		if err != nil {
			return err
		}
		if len(events) > 0 {
			restored, err := p.put(ctx, eventType.KNID, eventType.Code, eventType.DatasetID, events)
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
	return p.store.setSynced(ctx, eventType.KNID, eventType.Code, eventType.DatasetID, true)
}

// put writes events to the dataset of their type. A dataset that is gone is
// made again, and its type marked for the ledger's events to be written to
// it: restored then says the events given were not written.
func (p *projector) put(ctx context.Context, knID, code, datasetID string, events []*interfaces.KnEvent) (restored bool, err error) {
	if err = p.classes.write(ctx, datasetID, events); err == nil {
		return false, nil
	}
	if ctx.Err() != nil {
		return false, err
	}
	made, restoreErr := p.classes.restore(ctx, interfaces.KnEventType{KNID: knID, Code: code, DatasetID: datasetID})
	if restoreErr != nil || !made {
		return false, err
	}
	logger.Warnf("knowledge-network event classes: dataset %s of event type %s of network %s was gone and is made again",
		datasetID, code, knID)
	delete(p.resume, typeKey(knID, code))
	return true, p.store.setSynced(ctx, knID, code, datasetID, false)
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
// on only past what was written. An event of a type without a dataset is
// passed over: the type's events are written from the ledger once it has one.
// So is a check's refresh: it changes nothing a dataset keeps.
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
		datasets, err := p.store.datasetsOf(ctx, position.knID)
		if err != nil {
			return err
		}
		byType := map[string][]*interfaces.KnEvent{}
		seen := make(map[string]bool, len(changes))
		for index := range changes {
			event := &changes[index].Event
			if changes[index].Action == interfaces.KnEventChecked || seen[event.ID] || datasets[event.EventType] == "" {
				continue
			}
			seen[event.ID] = true
			byType[event.EventType] = append(byType[event.EventType], event)
		}
		for code, events := range byType {
			if _, err := p.put(ctx, position.knID, code, datasets[code], events); err != nil {
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
