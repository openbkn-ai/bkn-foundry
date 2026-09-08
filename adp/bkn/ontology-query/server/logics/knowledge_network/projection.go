// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package knowledge_network

import (
	"ontology-query/interfaces"
	"ontology-query/logics"
)

// levelObjectFromProjectedRow trusts only the system identity emitted by the
// object property-access plan. It never rebuilds identity from raw, masked, or
// partially visible primary-key properties.
func levelObjectFromProjectedRow(row map[string]any, objects interfaces.Objects) (interfaces.LevelObject, bool) {
	if objects.ObjectType == nil {
		return interfaces.LevelObject{}, false
	}
	instanceID, _ := row[interfaces.SYSTEM_PROPERTY_INSTANCE_ID].(string)
	identity, _ := row[interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY].(map[string]any)
	if instanceID == "" {
		return interfaces.LevelObject{}, false
	}
	return interfaces.LevelObject{
		ObjectID:   instanceID,
		ObjectUK:   identity,
		ObjectData: row,
		ObjectType: objects.ObjectType,
	}, true
}

func objectInfoFromLevelObject(levelObject interfaces.LevelObject, excludeSystemProperties []string) interfaces.ObjectInfoInSubgraph {
	info := interfaces.ObjectInfoInSubgraph{
		ObjectTypeId:   levelObject.ObjectType.OTID,
		ObjectTypeName: levelObject.ObjectType.OTName,
		Properties:     responseProperties(levelObject.ObjectData),
	}
	if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_ID, excludeSystemProperties) {
		info.InstanceID = levelObject.ObjectID
	}
	if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY, excludeSystemProperties) {
		info.InstanceIdentity = levelObject.ObjectUK
	}
	if !logics.ShouldExcludeSystemProperty(interfaces.SYSTEM_PROPERTY_DISPLAY, excludeSystemProperties) {
		info.Display = levelObject.ObjectData[interfaces.SYSTEM_PROPERTY_DISPLAY]
	}
	return info
}

func responseProperties(row map[string]any) map[string]any {
	properties := make(map[string]any, len(row))
	for name, value := range row {
		switch name {
		case interfaces.SYSTEM_PROPERTY_INSTANCE_ID,
			interfaces.SYSTEM_PROPERTY_INSTANCE_IDENTITY,
			interfaces.SYSTEM_PROPERTY_DISPLAY,
			interfaces.SORT_FIELD_SCORE:
			continue
		default:
			properties[name] = value
		}
	}
	return properties
}
