// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A project started on a network that already runs a version builds on what that network has:
// its action types, functions, Skills and object metrics are listed on the kinetic page beside the
// project's needs, and the kinetic analysis is given them, so that only what they leave uncovered
// becomes a new need. A network the project created has none of its own before the project.

// networkItemPrefix starts the id a network item is given by in the kinetic analysis: it goes as
// a kept need, which the Agent never repeats.
const networkItemPrefix = keptPrefix + "network-"

// releasesPage is how many releases are read at a time looking for a lineage's first.
const releasesPage = 100

// startedOnNetwork tells whether the network ran a version before the project's lineage first
// released to it: then what it has was there before the project. Before the first release that is
// whether it runs one now; running tells it, as runsVersion read it.
func (s *requirementService) startedOnNetwork(ctx context.Context, lineageID string, released bool,
	running bool) (bool, error) {
	if !released {
		return running, nil
	}
	first, err := s.firstRelease(ctx, lineageID)
	if err != nil || first == nil {
		return false, err
	}
	// A network the project created runs as version 1 what its first release published.
	return first.KNVersion > 1, nil
}

// firstRelease is the lineage's oldest published release; nil when there is none.
func (s *requirementService) firstRelease(ctx context.Context, lineageID string) (*interfaces.Release, error) {
	var oldest *interfaces.Release
	query := interfaces.ReleasesQuery{LineageID: lineageID, State: interfaces.RELEASE_STATE_PUBLISHED, Limit: releasesPage}
	for {
		page, err := s.rla.ListReleases(ctx, query)
		if err != nil {
			return nil, err
		}
		if len(page) > 0 {
			oldest = page[len(page)-1]
			query.Cursor = &interfaces.ReleaseCursor{CreateTime: oldest.CreateTime, ID: oldest.ID}
		}
		if len(page) < releasesPage {
			return oldest, nil
		}
	}
}

// featureNotServed is bkn-backend's plain 404: the network does not serve the feature, as object
// metrics without the professional edition. It has none of them then.
func featureNotServed(err error) bool {
	var backendErr *interfaces.BknBackendError
	return errors.As(err, &backendErr) && backendErr.HTTPCode == http.StatusNotFound && backendErr.ErrorCode == ""
}

// networkItems are the action types, functions, Skills and object metrics the live network has,
// each with the needs linked to it; reason says some list could not be read, and what of the
// others could is listed.
func (r *liveReader) networkItems(needs []*interfaces.RequirementLogicNeed) ([]*interfaces.LogicNetworkItem, string) {
	items := []*interfaces.LogicNetworkItem{}
	reason := ""
	failed := func(err error) {
		if reason == "" {
			reason = unreadable(err).Reason
		}
	}
	if actionTypes, err := r.listActionTypes(); err != nil {
		failed(err)
	} else {
		for _, a := range actionTypes {
			items = append(items, &interfaces.LogicNetworkItem{Type: interfaces.LOGIC_LINK_ACTION_TYPE, ID: a.ID,
				Name: a.Name, OwnerObjectTypeID: a.ObjectTypeID})
		}
	}
	for _, linkType := range []string{interfaces.LOGIC_LINK_FUNCTION, interfaces.LOGIC_LINK_SKILL} {
		capabilities, err := r.listCapabilities(linkType, "")
		if err != nil {
			failed(err)
			continue
		}
		for _, c := range capabilities {
			items = append(items, &interfaces.LogicNetworkItem{Type: linkType, ID: c.CapabilityID, BoxID: c.BoxID,
				Name: c.Name, Description: c.Description})
		}
	}
	if metrics, err := r.s.bba.ListObjectMetrics(r.ctx, r.token, r.network, ""); err != nil {
		if !featureNotServed(err) {
			failed(err)
		}
	} else {
		for _, m := range metrics {
			items = append(items, &interfaces.LogicNetworkItem{Type: interfaces.LOGIC_LINK_OBJECT_METRIC, ID: m.ID,
				Name: m.Name, Description: m.Description, OwnerObjectTypeID: m.OwnerObjectTypeID,
				OwnerObjectTypeName: m.OwnerObjectTypeName})
		}
	}
	for _, item := range items {
		item.NeedIDs = []string{}
		for _, n := range needs {
			// A link of another kind than the need's, from before its kind was changed, does not hold.
			if n.Link != nil && n.Link.Type == interfaces.LogicLinkTypeOf(n.Kind) && item.Implements(n.Link) {
				item.NeedIDs = append(item.NeedIDs, n.ID)
			}
		}
	}
	return items, reason
}

// networkNeedKind is the kind of need an item of the network implements.
func networkNeedKind(linkType string) string {
	switch linkType {
	case interfaces.LOGIC_LINK_OBJECT_METRIC:
		return interfaces.LOGIC_NEED_METRIC
	case interfaces.LOGIC_LINK_FUNCTION:
		return interfaces.LOGIC_NEED_FUNCTION
	case interfaces.LOGIC_LINK_SKILL:
		return interfaces.LOGIC_NEED_SKILL
	case interfaces.LOGIC_LINK_ACTION_TYPE:
		return interfaces.LOGIC_NEED_ACTION_TYPE
	}
	return ""
}

// messageNetworkItemsOf gives the network's items to the kinetic analysis as kept needs, by
// networkItemPrefix and their place: the Agent builds on them and never repeats them.
func messageNetworkItemsOf(items []*interfaces.LogicNetworkItem) []messageLogicNeed {
	out := []messageLogicNeed{}
	for i, item := range items {
		n := messageLogicNeed{ID: networkItemPrefix + strconv.Itoa(i+1), Kind: networkNeedKind(item.Type),
			Name: item.Name, Definition: item.Description}
		if item.OwnerObjectTypeID != "" {
			n.ObjectTypeIDs = []string{item.OwnerObjectTypeID}
		}
		out = append(out, n)
	}
	return out
}

// coveringItem is the network's item a need of kind named name is covered by: one of the kind of
// implementation the need links to and of the same name. An API need is left to a person, as only
// a function bound from an OpenAPI description implements it.
func coveringItem(items []*interfaces.LogicNetworkItem, kind string, name string) *interfaces.LogicNetworkItem {
	linkType := interfaces.LogicLinkTypeOf(kind)
	if linkType == "" || kind == interfaces.LOGIC_NEED_API || squeezed(name) == "" {
		return nil
	}
	for _, item := range items {
		if item.Type == linkType && squeezed(item.Name) == squeezed(name) {
			return item
		}
	}
	return nil
}

// uncovered leaves out of proposed the needs the network's items already cover: one the Agent
// gives again by an item's id, and a new one of the same kind and name as an item. A kept need
// given again stays, to be linked with the rest.
func uncovered(proposed []agentLogicNeed, items []*interfaces.LogicNetworkItem) []agentLogicNeed {
	if len(items) == 0 {
		return proposed
	}
	out := []agentLogicNeed{}
	for _, p := range proposed {
		id := strings.TrimSpace(p.ID)
		switch {
		case strings.HasPrefix(id, networkItemPrefix):
		case !strings.HasPrefix(id, keptPrefix) && coveringItem(items, p.Kind, p.Name) != nil:
		default:
			out = append(out, p)
		}
	}
	return out
}

// linkCovered links each need no one linked or changed to the network's item that covers it, by
// caller at now.
func linkCovered(spec *interfaces.RequirementSpec, items []*interfaces.LogicNetworkItem,
	caller interfaces.AccountInfo, now int64) {
	for _, n := range spec.LogicNeeds {
		if n.Link != nil || n.Edited {
			continue
		}
		if item := coveringItem(items, n.Kind, n.Name); item != nil {
			n.Link = &interfaces.RequirementLogicLink{Type: item.Type, ID: item.ID, BoxID: item.BoxID, By: caller, At: now}
		}
	}
}
