// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

// AI-assisted kinetic modeling and business scenarios. A project keeps one supplement note, the
// owner's own words on what the project is for, that every kinetic analysis, scenario drafting and
// Ask AI turn reads; it is stored beside the specification (t_bkn_project_requirement.f_supplement),
// never in it, so running again never clears it. Ask AI conversations are kept per page in
// t_bkn_project_requirement_conversation, one row each: questions, answers and the proposals each
// answer made, with what became of them. Adopting a proposal writes it into the specification as
// any edit does; deleting a conversation leaves what was adopted.

// The pages a conversation belongs to.
const (
	REQUIREMENT_PAGE_KINETIC   = "kinetic"
	REQUIREMENT_PAGE_SCENARIOS = "scenarios"
)

// Statuses of a turn: answered in the background, it is pending until the Agent answers.
const (
	REQUIREMENT_TURN_PENDING = "pending"
	REQUIREMENT_TURN_READY   = "ready"
	REQUIREMENT_TURN_FAILED  = "failed"
)

// What a proposal does to the specification.
const (
	REQUIREMENT_PROPOSAL_ADD    = "add"
	REQUIREMENT_PROPOSAL_MODIFY = "modify"
	REQUIREMENT_PROPOSAL_MERGE  = "merge"
	REQUIREMENT_PROPOSAL_DELETE = "delete"
	REQUIREMENT_PROPOSAL_LINK   = "link"
	REQUIREMENT_PROPOSAL_SPLIT  = "split"
)

// What a person did with a proposal; empty while undecided.
const (
	REQUIREMENT_PROPOSAL_ADOPTED  = "adopted"
	REQUIREMENT_PROPOSAL_DECLINED = "declined"
)

// REQUIREMENT_SOURCE_ASK is the source of an item adopted from Ask AI: of a kinetic need, and the
// optional source of a scenario, question or rule.
const REQUIREMENT_SOURCE_ASK = "ask"

// Bounds of what people write to Ask AI and in the supplement note, in characters.
const (
	REQUIREMENT_SUPPLEMENT_MAX_LENGTH = 2000
	REQUIREMENT_QUESTION_MAX_LENGTH   = 2000
	REQUIREMENT_ASK_REFS_MAX          = 20
)

// RequirementSupplement is the project's supplement note: empty text when none was written. By is
// who wrote it last; its name is filled in replies only.
type RequirementSupplement struct {
	Text string             `json:"text"`
	By   *RequirementAuthor `json:"by,omitempty"`
	Time int64              `json:"time"`
}

// RequirementAuthor is an account with its display name, named only in replies.
type RequirementAuthor struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// RequirementSupplementEdit replaces the supplement note; empty text clears it.
type RequirementSupplementEdit struct {
	Text string `json:"text"`
}

// RequirementConversation is one Ask AI conversation of a page.
type RequirementConversation struct {
	ID         string                         `json:"id"`
	ProjectID  string                         `json:"project_id"`
	Page       string                         `json:"page"`
	Title      string                         `json:"title"`
	Turns      []*RequirementConversationTurn `json:"turns"`
	Creator    AccountInfo                    `json:"creator"`
	CreateTime int64                          `json:"create_time"`
	UpdateTime int64                          `json:"update_time"`
}

// RequirementConversationTurn is a question with its answer and the proposals the answer made.
// Error is why it failed, as a code for the client to word. RunID tells the run that answers it.
type RequirementConversationTurn struct {
	ID         string                 `json:"id"`
	Question   string                 `json:"question"`
	Refs       []RequirementAskRef    `json:"refs"`
	Status     string                 `json:"status"`
	Error      string                 `json:"error,omitempty"`
	Answer     string                 `json:"answer"`
	Proposals  []*RequirementProposal `json:"proposals"`
	Dropped    int                    `json:"dropped"`
	RunID      string                 `json:"run_id,omitempty"`
	By         AccountInfo            `json:"by"`
	CreateTime int64                  `json:"create_time"`
	UpdateTime int64                  `json:"update_time"`
}

// RequirementAskRef is an item of the list a question refers to, with its name as it was asked.
type RequirementAskRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// RequirementProposal is a change Ask AI proposed, checked against the specification as it stood
// when asked. Before and IntoBefore are the items it changes as they were then: adopting it once
// they changed is refused.
type RequirementProposal struct {
	Op          string                      `json:"op"`
	Kind        string                      `json:"kind"`
	TargetID    string                      `json:"target_id,omitempty"`
	IntoID      string                      `json:"into_id,omitempty"`
	Before      any                         `json:"before,omitempty"`
	IntoBefore  any                         `json:"into_before,omitempty"`
	Item        *RequirementProposalItem    `json:"item,omitempty"`
	Parts       []RequirementProposalItem   `json:"parts,omitempty"`
	Moves       []RequirementProposalMove   `json:"moves,omitempty"`
	NetworkItem *RequirementProposalNetwork `json:"network_item,omitempty"`
	Reason      string                      `json:"reason"`
	Status      string                      `json:"status"`
	ResultID    string                      `json:"result_id,omitempty"`
	// Fingerprint signs Before and IntoBefore as stored, to tell they changed since.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// RequirementProposalItem is an item a proposal adds, or the fields it changes, by kind: the
// kind, name, definition and object types of a kinetic need; the name, actor, trigger, decision
// and knowledge of a scenario; the text, scenarios and kinetic needs of a question; the statement
// of a rule.
type RequirementProposalItem struct {
	LogicKind     string   `json:"logic_kind,omitempty"`
	Name          string   `json:"name,omitempty"`
	Definition    string   `json:"definition,omitempty"`
	ObjectTypeIDs []string `json:"object_type_ids,omitempty"`
	Actor         string   `json:"actor,omitempty"`
	Trigger       string   `json:"trigger,omitempty"`
	Decision      string   `json:"decision,omitempty"`
	Knowledge     []string `json:"knowledge,omitempty"`
	Text          string   `json:"text,omitempty"`
	ScenarioIDs   []string `json:"scenario_ids,omitempty"`
	LogicNeedIDs  []string `json:"logic_need_ids,omitempty"`
	Statement     string   `json:"statement,omitempty"`
}

// RequirementProposalMove puts a question of a scenario split into one of its parts.
type RequirementProposalMove struct {
	QuestionID string `json:"question_id"`
	Part       int    `json:"part"`
}

// RequirementProposalNetwork is the item of the network a kinetic need is linked to.
type RequirementProposalNetwork struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	BoxID string `json:"box_id,omitempty"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
}

// RequirementAskRequest asks a question in a conversation: Page starts a new one.
type RequirementAskRequest struct {
	Page     string              `json:"page"`
	Question string              `json:"question"`
	Refs     []RequirementAskRef `json:"refs"`
}

// RequirementProposalsDecision adopts and declines proposals of one turn, adopting over the
// specification as read at UpdateTime.
type RequirementProposalsDecision struct {
	UpdateTime int64 `json:"update_time"`
	Adopt      []int `json:"adopt"`
	Decline    []int `json:"decline"`
}

// RequirementProposalsResult is the conversation after a decision, and the specification after
// it when something was adopted.
type RequirementProposalsResult struct {
	Conversation *RequirementConversation `json:"conversation"`
	Requirement  *Requirement             `json:"requirement"`
}
