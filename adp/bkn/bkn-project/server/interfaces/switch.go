// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// A switch moves a lineage's consumers from one of its networks to another.
// Agents, MCP configurations, workshop and eval are switched by their owners;
// bkn-project moves the schedules and records the switch. The network in use
// is the target of the latest switch, or the lineage's base network before
// any. A rollback is a switch back to an earlier network.

// Switch is one row of t_bkn_project_switch.
type Switch struct {
	ID            string             `json:"id"`
	LineageID     string             `json:"lineage_id"`
	FromNetworkID string             `json:"from_network_id"`
	ToNetworkID   string             `json:"to_network_id"`
	Operator      AccountInfo        `json:"operator"`
	Schedules     []SwitchedSchedule `json:"schedules"`
	CreateTime    int64              `json:"create_time"`
}

// SwitchedSchedule is a schedule a switch moved: it was turned off on the
// network switched away from and runs as ToID on the network switched to.
type SwitchedSchedule struct {
	FromID string `json:"from_id"`
	ToID   string `json:"to_id"`
	Name   string `json:"name"`
}
