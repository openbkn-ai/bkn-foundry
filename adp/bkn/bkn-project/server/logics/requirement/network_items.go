// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
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

// maxMessageNetworkItems bounds the network's items given to the kinetic analysis: a large network
// may have many. Those on the model's object types go first; the server still checks every
// proposal against all of them.
const maxMessageNetworkItems = 200

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
// others could is listed. Functions and Skills are those the network mounts, as its statistics
// count them: a tool its model only uses, as an action type runs one, is no capability of its own.
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
			items = append(items, &interfaces.LogicNetworkItem{Type: interfaces.LOGIC_LINK_ACTION_TYPE,
				Kind: interfaces.LOGIC_NEED_ACTION_TYPE, ID: a.ID, Name: a.Name, Description: a.Comment,
				OwnerObjectTypeID: a.ObjectTypeID})
		}
	}
	for _, linkType := range []string{interfaces.LOGIC_LINK_FUNCTION, interfaces.LOGIC_LINK_SKILL} {
		capabilities, err := r.listCapabilities(linkType, "")
		if err != nil {
			failed(err)
			continue
		}
		for _, c := range capabilities {
			if c.ID == "" {
				continue
			}
			kind := networkNeedKind(linkType)
			if c.MetadataType == capabilityMetadataType(interfaces.LOGIC_NEED_API) {
				kind = interfaces.LOGIC_NEED_API
			}
			items = append(items, &interfaces.LogicNetworkItem{Type: linkType, Kind: kind, ID: c.CapabilityID, BoxID: c.BoxID,
				Name: c.Name, Description: c.Description})
		}
	}
	if metrics, err := r.s.bba.ListObjectMetrics(r.ctx, r.token, r.network, ""); err != nil {
		if !featureNotServed(err) {
			failed(err)
		}
	} else {
		for _, m := range metrics {
			items = append(items, &interfaces.LogicNetworkItem{Type: interfaces.LOGIC_LINK_OBJECT_METRIC,
				Kind: interfaces.LOGIC_NEED_METRIC, ID: m.ID,
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

// modelNames are the names of the object types of the model, by their ids.
func modelNames(objectTypes []messageObjectType) map[string]string {
	names := map[string]string{}
	for _, ot := range objectTypes {
		names[ot.ID] = ot.Name
	}
	return names
}

// messageNetworkItemsOf gives the network's items to the kinetic analysis as kept needs, by
// networkItemPrefix and their place, each with its description and the object type it belongs to:
// the Agent builds on them and never repeats them. At most maxMessageNetworkItems go, those on an
// object type of model, the names of its object types by their ids, first; it counts those left out.
func messageNetworkItemsOf(items []*interfaces.LogicNetworkItem, model map[string]string) ([]messageLogicNeed, int) {
	order := make([]int, 0, len(items))
	for pass := range 2 {
		for i, item := range items {
			_, onModel := model[item.OwnerObjectTypeID]
			if onModel == (pass == 0) {
				order = append(order, i)
			}
		}
	}
	out := []messageLogicNeed{}
	for _, i := range order[:min(len(order), maxMessageNetworkItems)] {
		item := items[i]
		kind := item.Kind
		if kind == "" {
			kind = networkNeedKind(item.Type)
		}
		n := messageLogicNeed{ID: networkItemPrefix + strconv.Itoa(i+1), Kind: kind,
			Name: item.Name, Definition: item.Description}
		if item.OwnerObjectTypeID != "" {
			n.ObjectTypeIDs = []string{item.OwnerObjectTypeID}
			n.OwnerObjectType = cmp.Or(item.OwnerObjectTypeName, model[item.OwnerObjectTypeID])
		}
		out = append(out, n)
	}
	return out, len(items) - len(out)
}

// networkItemOf is the network's item a kept-network- id gives, by its place; nil for another id.
func networkItemOf(items []*interfaces.LogicNetworkItem, id string) *interfaces.LogicNetworkItem {
	place, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(id), networkItemPrefix))
	if !strings.HasPrefix(strings.TrimSpace(id), networkItemPrefix) || err != nil || place < 1 || place > len(items) {
		return nil
	}
	return items[place-1]
}

// coveringItems are the network's items a need of kind named name is covered by: those of the kind
// of implementation the need links to and of the same name. An API need is left to a person, as
// only a function bound from an OpenAPI description implements it.
func coveringItems(items []*interfaces.LogicNetworkItem, kind string, name string) []*interfaces.LogicNetworkItem {
	linkType := interfaces.LogicLinkTypeOf(kind)
	if linkType == "" || kind == interfaces.LOGIC_NEED_API || squeezed(name) == "" {
		return nil
	}
	var out []*interfaces.LogicNetworkItem
	for _, item := range items {
		if item.Type == linkType && squeezed(item.Name) == squeezed(name) {
			out = append(out, item)
		}
	}
	return out
}

// coveringItem is the one item covering need: the only one of its name, or of those the only one
// on an object type of the need. Items of the same name in several toolboxes or on several object
// types are left to a person.
func coveringItem(items []*interfaces.LogicNetworkItem, need *interfaces.RequirementLogicNeed) *interfaces.LogicNetworkItem {
	named := coveringItems(items, need.Kind, need.Name)
	if len(named) > 1 {
		named = slices.DeleteFunc(named, func(item *interfaces.LogicNetworkItem) bool {
			return !slices.Contains(need.ObjectTypeIDs, item.OwnerObjectTypeID)
		})
	}
	if len(named) != 1 {
		return nil
	}
	return named[0]
}

// uncovered leaves out of proposed the needs the network's items already cover: one the Agent
// gives again by an item's id, one it says an item covers, and a new one of the same kind and name
// as an item. A kept need given again stays, to be linked with the rest; covered gives, by their
// ids, the kept needs the Agent said an item covers, with that item.
func uncovered(proposed []agentLogicNeed, items []*interfaces.LogicNetworkItem) (
	[]agentLogicNeed, map[string]*interfaces.LogicNetworkItem) {
	covered := map[string]*interfaces.LogicNetworkItem{}
	if len(items) == 0 {
		return proposed, covered
	}
	out := []agentLogicNeed{}
	for _, p := range proposed {
		id := strings.TrimSpace(p.ID)
		by := networkItemOf(items, p.CoveredBy)
		kept := strings.HasPrefix(id, keptPrefix) && !strings.HasPrefix(id, networkItemPrefix)
		if kept && by != nil {
			covered[strings.TrimPrefix(id, keptPrefix)] = by
		}
		switch {
		case strings.HasPrefix(id, networkItemPrefix):
		case !kept && (by != nil || len(coveringItems(items, p.Kind, p.Name)) > 0):
		default:
			out = append(out, p)
		}
	}
	return out, covered
}

// linkCovered links each need no one linked or changed to the network's item that covers it, by
// caller at now: the one covered gives for it by its id, when that item implements needs of its
// kind, or the one of its name. An API need is left to a person, as coveringItems leaves it.
func linkCovered(spec *interfaces.RequirementSpec, items []*interfaces.LogicNetworkItem,
	covered map[string]*interfaces.LogicNetworkItem, caller interfaces.AccountInfo, now int64) {
	for _, n := range spec.LogicNeeds {
		if n.Link != nil || n.Edited {
			continue
		}
		item := covered[n.ID]
		if item == nil || n.Kind == interfaces.LOGIC_NEED_API || cmp.Or(item.Kind, networkNeedKind(item.Type)) != n.Kind {
			item = coveringItem(items, n)
		}
		if item != nil {
			n.Link = &interfaces.RequirementLogicLink{Type: item.Type, ID: item.ID, BoxID: item.BoxID, By: caller, At: now}
		}
	}
}
