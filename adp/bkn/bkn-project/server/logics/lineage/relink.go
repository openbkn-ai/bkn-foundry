// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package lineage

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-backend/server/bkn-specification/bkn"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/otellog"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/logics/capability"
)

// maxRelinks bounds how many gone toolboxes and Skills one release check follows.
const maxRelinks = 32

// relinkGone moves the package's references to the toolbox or Skill gone names onto the one that
// took its place, as a table made again is bound again by its name: for a toolbox, the one the
// network's own action types of the same ids use now, else the one published toolbox of the name
// the network records for it; for a Skill, the published Skill of its name. It returns nil when
// none or several would do, or a tool cannot be matched, and the release review names the gone
// item as before. The network is only read; the move is in the package the release writes.
func (s *lineageService) relinkGone(ctx context.Context, token string, lineageID string, pkg map[string][]byte,
	gone *capability.ItemError) (map[string][]byte, *interfaces.CapabilityRelink) {
	network, err := loadNetwork(pkg)
	if err != nil {
		return nil, nil
	}
	name, _ := capability.Uses(network, gone.Kind, gone.ID)
	var c *capability.Copy
	by := interfaces.RELINK_BY_NAME
	switch gone.Kind {
	case capability.KIND_FUNCTION:
		c, by = s.relinkFunction(ctx, token, lineageID, network, gone.ID, name)
	case capability.KIND_SKILL:
		if name == "" {
			return nil, nil
		}
		id, status, err := s.efa.FindSkill(ctx, token, name)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Find the Skill named %s: %v", name, err))
			return nil, nil
		}
		if id != "" && id != gone.ID && status == interfaces.SKILL_STATUS_PUBLISHED {
			c = capability.RelinkSkill(gone.ID, id, name)
		}
	}
	if c == nil {
		return nil, nil
	}
	rewritten, err := capability.RewritePackage(pkg, lineageID, lineageID, []*capability.Copy{c})
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Relink %s %s to %s: %v", gone.Kind, gone.ID, c.NewID, err))
		return nil, nil
	}
	if again, err := loadNetwork(rewritten); err != nil {
		return nil, nil
	} else if boxIDs, skillIDs := capability.References(again); slices.Contains(boxIDs, gone.ID) ||
		slices.Contains(skillIDs, gone.ID) {
		return nil, nil
	}
	otellog.LogInfo(ctx, fmt.Sprintf("Release of %s relinks %s %s (%s) to %s by %s", lineageID, gone.Kind, gone.ID,
		name, c.NewID, by))
	if c.NewName != "" {
		name = c.NewName
	}
	return rewritten, &interfaces.CapabilityRelink{Kind: string(gone.Kind), Name: name, PreviousID: gone.ID,
		ID: c.NewID, By: by}
}

// relinkFunction finds the toolbox that took the place of toolbox oldID and matches its tools: the
// one the network's action types of the same ids use now, else the one published toolbox named name.
func (s *lineageService) relinkFunction(ctx context.Context, token string, lineageID string,
	network *bkn.BknNetwork, oldID string, name string) (*capability.Copy, string) {
	newID, direct, ok := s.networkToolbox(ctx, token, lineageID, network, oldID)
	if !ok {
		return nil, ""
	}
	by := interfaces.RELINK_BY_NETWORK
	if newID == "" {
		if name == "" {
			return nil, ""
		}
		ids, err := s.efa.FindToolboxes(ctx, token, name)
		if err != nil {
			otellog.LogWarn(ctx, fmt.Sprintf("Find the toolboxes named %s: %v", name, err))
			return nil, ""
		}
		ids = slices.DeleteFunc(ids, func(id string) bool { return id == oldID })
		if len(ids) != 1 {
			return nil, ""
		}
		newID, direct, by = ids[0], nil, interfaces.RELINK_BY_NAME
	}
	to, err := capability.ExportFunction(ctx, s.efa, token, newID)
	if err != nil {
		otellog.LogWarn(ctx, fmt.Sprintf("Read toolbox %s to relink %s: %v", newID, oldID, err))
		return nil, ""
	}
	c, err := capability.RelinkFunction(network, oldID, to, direct)
	if err != nil {
		otellog.LogInfo(ctx, fmt.Sprintf("Toolbox %s is not relinked: %v", oldID, err))
		return nil, ""
	}
	return c, by
}

// networkToolbox reads the network's own action types that the package's action types on toolbox
// oldID share ids with: the one toolbox they use now in its place, "" when they name none, and the
// tool each uses in place of the old one. ok is false when they name several.
func (s *lineageService) networkToolbox(ctx context.Context, token string, lineageID string,
	network *bkn.BknNetwork, oldID string) (newID string, direct map[string]string, ok bool) {
	if s.bba == nil {
		return "", nil, true
	}
	var used []*bkn.BknActionType
	for _, at := range network.ActionTypes {
		if src := at.ActionSource; src != nil && strings.EqualFold(src.Type, "tool") && src.BoxID == oldID {
			used = append(used, at)
		}
	}
	if len(used) == 0 {
		return "", nil, true
	}
	live, err := s.bba.ListActionTypes(ctx, token, lineageID, "")
	if err != nil {
		// The name still finds it.
		otellog.LogWarn(ctx, fmt.Sprintf("Read the action types of network %s: %v", lineageID, err))
		return "", nil, true
	}
	now := map[string]interfaces.KNActionTypeSource{}
	for _, at := range live {
		if strings.EqualFold(at.Source.Type, "tool") && at.Source.BoxID != "" && at.Source.BoxID != oldID {
			now[at.ID] = at.Source
		}
	}
	direct = map[string]string{}
	for _, at := range used {
		src, found := now[at.ID]
		if !found {
			continue
		}
		if newID != "" && newID != src.BoxID {
			return "", nil, false
		}
		newID = src.BoxID
		if old := at.ActionSource.ToolID; old != "" {
			if prev, seen := direct[old]; seen && prev != src.ToolID {
				return "", nil, false
			}
			direct[old] = src.ToolID
		}
	}
	return newID, direct, true
}
