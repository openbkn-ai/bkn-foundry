// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package interfaces

import (
	"context"
	"errors"
)

// A project's requirement specification, rq-n, says what the knowledge network is for: business
// scenarios, candidate concepts, scenario questions and business rules, drafted by bkn-agent from
// the requirement documents the owner uploaded. Every drafted item quotes the documents, or says
// why it has no basis in them. Conflicts between documents and modelling choices are listed for
// the owner to settle; the agent proposes, rules check, people decide.

// Requirement statuses as stored. A project without a row has not drafted one yet.
const (
	REQUIREMENT_STATUS_DRAFTING = "drafting"
	REQUIREMENT_STATUS_READY    = "ready"
	REQUIREMENT_STATUS_FAILED   = "failed"
)

// Requirement document types, in the order of authority for terms and rules.
const (
	REQUIREMENT_DOC_STANDARD = "standard"
	REQUIREMENT_DOC_POLICY   = "policy"
	REQUIREMENT_DOC_PRD      = "prd"
	REQUIREMENT_DOC_OTHER    = "other"
)

// IsRequirementDocType tells whether docType is a requirement document type.
func IsRequirementDocType(docType string) bool {
	switch docType {
	case REQUIREMENT_DOC_STANDARD, REQUIREMENT_DOC_POLICY, REQUIREMENT_DOC_PRD, REQUIREMENT_DOC_OTHER:
		return true
	}
	return false
}

// Requirement document formats.
const (
	REQUIREMENT_FORMAT_MARKDOWN = "markdown"
	REQUIREMENT_FORMAT_TEXT     = "text"
)

// Question layers: a knowledge question is answered by the ontology and its facts, and is asked
// again over MCP after a release; a capability question needs a metric, function or skill that
// another team builds.
const (
	REQUIREMENT_LAYER_KNOWLEDGE  = "knowledge"
	REQUIREMENT_LAYER_CAPABILITY = "capability"
)

// Where a question's needs come from: its concepts' drafted items, or a person who set them.
const (
	REQUIREMENT_NEEDS_FROM_CONCEPTS = "concepts"
	REQUIREMENT_NEEDS_FROM_PERSON   = "person"
)

// Kinds of drafted items a concept maps to or a question needs.
const (
	REQUIREMENT_ITEM_OBJECT_TYPE   = "object_type"
	REQUIREMENT_ITEM_RELATION_TYPE = "relation_type"
)

// ErrRequirementHasDecisions is returned by StartDrafting when drafting again would discard the
// conflict resolutions and modelling choices people made and the caller did not agree to that.
var ErrRequirementHasDecisions = errors.New("requirement specification has decisions")

// RequirementDocument is one row of t_bkn_project_requirement_document. Lists leave out the
// content and blocks.
type RequirementDocument struct {
	ID         string             `json:"id"`
	ProjectID  string             `json:"project_id"`
	Name       string             `json:"name"`
	DocType    string             `json:"doc_type"`
	Format     string             `json:"format"`
	Content    string             `json:"content,omitempty"`
	Blocks     []RequirementBlock `json:"blocks,omitempty"`
	SHA256     string             `json:"sha256"`
	Size       int64              `json:"size"`
	Uploader   AccountInfo        `json:"uploader"`
	CreateTime int64              `json:"create_time"`
	UpdateTime int64              `json:"update_time"`
}

// RequirementBlock is a part of a document's content between headings or blank lines. Start and
// End are character offsets into the content.
type RequirementBlock struct {
	ID      string `json:"id"`
	Heading string `json:"heading,omitempty"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
}

// Requirement is one row of t_bkn_project_requirement: the project's current specification.
type Requirement struct {
	ProjectID string `json:"project_id"`
	// Version is n of rq-n.
	Version int             `json:"version"`
	Status  string          `json:"status"`
	Spec    RequirementSpec `json:"spec"`
	// Inputs records what the specification was drafted from, to tell when it is out of date.
	Inputs *RequirementInputs `json:"inputs,omitempty"`
	// Error is why drafting failed, as a code for the client to word.
	Error string `json:"error,omitempty"`
	// RunID tells drafting runs apart: only the run that started drafting records its outcome.
	RunID string `json:"-"`
	// AgentTaskID is the bkn-agent task of the drafting.
	AgentTaskID string      `json:"-"`
	Drafter     AccountInfo `json:"drafter"`
	CreateTime  int64       `json:"create_time"`
	UpdateTime  int64       `json:"update_time"`
}

// RequirementInputs is what drafting read: the documents with their content hashes and types, the
// project's catalogs and naming language, and the version of the checking rules.
type RequirementInputs struct {
	RulesVersion   int                        `json:"rules_version"`
	Documents      []RequirementInputDocument `json:"documents"`
	CatalogIDs     []string                   `json:"catalog_ids"`
	NamingLanguage string                     `json:"naming_language"`
}

type RequirementInputDocument struct {
	ID      string `json:"id"`
	SHA256  string `json:"sha256"`
	DocType string `json:"doc_type"`
}

// RequirementSpec is the specification. Item ids are the server's: s1, c1, q1, r1, k1 and f1.
type RequirementSpec struct {
	Scenarios []*RequirementScenario `json:"scenarios"`
	Concepts  []*RequirementConcept  `json:"concepts"`
	Questions []*RequirementQuestion `json:"questions"`
	Rules     []*RequirementRule     `json:"rules"`
	Conflicts []*RequirementConflict `json:"conflicts"`
	Choices   []*RequirementChoice   `json:"choices"`
	// Rejected are the drafted items the checks dropped, with why, so none is dropped unseen.
	Rejected []*RequirementRejected `json:"rejected"`
	// TablesOmitted counts the tables of the project's catalogs drafting did not name to the Agent:
	// it names at most a set number, the first ones listed. Absent when none was left out.
	TablesOmitted int `json:"tables_omitted,omitempty"`
	// CatalogsUnlisted are the project's catalogs whose tables drafting could not list, so none of
	// their tables went to the Agent. Absent when every catalog was listed.
	CatalogsUnlisted []string `json:"catalogs_unlisted,omitempty"`
	// LastIDs is the highest number given to an item of each kind (s, c, q, r) since drafting, so
	// an id once given, even of an item removed since, is never given again.
	LastIDs map[string]int `json:"last_ids,omitempty"`
}

// RequirementCitation is a passage of a document an item quotes. The agent gives the document and
// the quote; the server finds the quote and records the block and offsets.
type RequirementCitation struct {
	DocumentID string `json:"document_id"`
	Quote      string `json:"quote"`
	BlockID    string `json:"block_id"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
}

// RequirementItemRef names a drafted object type or relation type by its draft item id, which
// becomes its BKN id when the draft is compiled.
type RequirementItemRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

type RequirementScenario struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Actor    string `json:"actor"`
	Trigger  string `json:"trigger"`
	Decision string `json:"decision"`
	// Knowledge is what the decision needs from this network.
	Knowledge []string `json:"knowledge"`
	// Capabilities are what other teams build: metrics with their definition, functions, skills
	// and applications.
	Capabilities []RequirementCapability `json:"capabilities"`
	Loop         *RequirementLoop        `json:"loop,omitempty"`
	Citations    []RequirementCitation   `json:"citations"`
	// SupplementReason says why an item without a basis in the documents is needed.
	SupplementReason string `json:"supplement_reason,omitempty"`
	// Edited is set once a person changed the item.
	Edited bool `json:"edited"`
}

type RequirementCapability struct {
	// Kind is metric, function, skill or app.
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Definition string `json:"definition,omitempty"`
}

// RequirementLoop is a scenario's operating loop, from what is discovered to what tracks it.
type RequirementLoop struct {
	Discover    string `json:"discover"`
	Judge       string `json:"judge"`
	Suggest     string `json:"suggest"`
	Confirm     string `json:"confirm"`
	Execute     string `json:"execute"`
	StateChange string `json:"state_change"`
	Track       string `json:"track"`
}

type RequirementConcept struct {
	ID   string `json:"id"`
	Term string `json:"term"`
	// Category is object, event, property, relation, evidence or not_ontology.
	Category          string `json:"category"`
	Definition        string `json:"definition"`
	NotOntologyReason string `json:"not_ontology_reason,omitempty"`
	// MapsTo is the drafted item the concept is, when it is one.
	MapsTo           *RequirementItemRef   `json:"maps_to,omitempty"`
	Citations        []RequirementCitation `json:"citations"`
	SupplementReason string                `json:"supplement_reason,omitempty"`
	Edited           bool                  `json:"edited"`
}

type RequirementQuestion struct {
	ID          string   `json:"id"`
	Text        string   `json:"text"`
	ScenarioIDs []string `json:"scenario_ids"`
	// Type is point_in_time, provenance, filter, compare, aggregate, count, disambiguate, judge
	// or metric.
	Type  string `json:"type"`
	Layer string `json:"layer"`
	// Template is the question with slots; AnswerShape what an answer holds. Neither has values.
	Template    string           `json:"template"`
	AnswerShape string           `json:"answer_shape"`
	Basis       RequirementBasis `json:"basis"`
	// Needs are the drafted items the question depends on; NeedsSource says whether they follow
	// its concepts or a person set them.
	Needs            []RequirementItemRef  `json:"needs"`
	NeedsSource      string                `json:"needs_source"`
	Citations        []RequirementCitation `json:"citations"`
	SupplementReason string                `json:"supplement_reason,omitempty"`
	Edited           bool                  `json:"edited"`
}

// RequirementBasis is what a question rests on, as bkn-eval's answer sets name it.
type RequirementBasis struct {
	ConceptIDs []string `json:"concept_ids"`
	Metrics    []string `json:"metrics"`
	Functions  []string `json:"functions"`
	Skills     []string `json:"skills"`
}

type RequirementRule struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	// CompilesTo is constraint, metric_definition, code_pattern, enum or other.
	CompilesTo string                `json:"compiles_to"`
	Citations  []RequirementCitation `json:"citations"`
	Edited     bool                  `json:"edited"`
}

type RequirementConflict struct {
	ID    string `json:"id"`
	Topic string `json:"topic"`
	// Kind is term, rule, goal, scope or scenario: it decides which document type prevails.
	Kind  string                    `json:"kind"`
	Sides []RequirementConflictSide `json:"sides"`
	// SuggestedDocumentID is the side the order of authority prefers; empty when the sides are
	// of equal authority.
	SuggestedDocumentID string                 `json:"suggested_document_id,omitempty"`
	Resolution          *RequirementResolution `json:"resolution,omitempty"`
	// Deferred is set when the owner handed the conflict to the business to confirm: it stays
	// unresolved.
	Deferred bool `json:"deferred"`
}

type RequirementConflictSide struct {
	RequirementCitation
	// Position is what this side says, in a few words.
	Position string `json:"position"`
}

type RequirementResolution struct {
	// DocumentID is the side chosen; empty when Text states another conclusion.
	DocumentID string      `json:"document_id,omitempty"`
	Text       string      `json:"text,omitempty"`
	Reason     string      `json:"reason"`
	By         AccountInfo `json:"by"`
	Time       int64       `json:"time"`
}

// RequirementChoice is a point where the ontology can be modelled more than one way. It never
// blocks: undecided, the recommended option holds.
type RequirementChoice struct {
	ID                  string                     `json:"id"`
	Question            string                     `json:"question"`
	Options             []RequirementOption        `json:"options"`
	RecommendedOptionID string                     `json:"recommended_option_id"`
	Reason              string                     `json:"reason"`
	Citations           []RequirementCitation      `json:"citations"`
	Decision            *RequirementChoiceDecision `json:"decision,omitempty"`
}

type RequirementOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Consequence string `json:"consequence"`
}

type RequirementChoiceDecision struct {
	OptionID string      `json:"option_id"`
	Basis    string      `json:"basis"`
	By       AccountInfo `json:"by"`
	Time     int64       `json:"time"`
}

// RequirementRejected is a drafted item a check dropped. Reason is a code: quote_not_found,
// number_not_in_quote or unknown_document.
type RequirementRejected struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Reason string `json:"reason"`
}

// HasDecisions tells whether people resolved, deferred or chose anything in the specification.
func (s *RequirementSpec) HasDecisions() bool {
	for _, c := range s.Conflicts {
		if c.Resolution != nil || c.Deferred {
			return true
		}
	}
	for _, c := range s.Choices {
		if c.Decision != nil {
			return true
		}
	}
	return false
}

// Kinds of specification items, as the routes name them.
const (
	REQUIREMENT_KIND_SCENARIOS = "scenarios"
	REQUIREMENT_KIND_CONCEPTS  = "concepts"
	REQUIREMENT_KIND_QUESTIONS = "questions"
	REQUIREMENT_KIND_RULES     = "rules"
)

// RequirementItemEdit changes the fields it sets of one item, for the item's kind: name, actor,
// trigger, decision and knowledge of a scenario; term, category and definition of a concept; text,
// layer, template, answer_shape, scenario_ids and needs of a question; statement of a rule.
// Setting a question's needs makes them the person's; follow_concepts makes them follow its
// concepts again. UpdateTime is the specification's as read.
type RequirementItemEdit struct {
	UpdateTime  int64                 `json:"update_time"`
	Name        *string               `json:"name"`
	Actor       *string               `json:"actor"`
	Trigger     *string               `json:"trigger"`
	Decision    *string               `json:"decision"`
	Knowledge   *[]string             `json:"knowledge"`
	Term        *string               `json:"term"`
	Category    *string               `json:"category"`
	Definition  *string               `json:"definition"`
	Text        *string               `json:"text"`
	Layer       *string               `json:"layer"`
	Template    *string               `json:"template"`
	AnswerShape *string               `json:"answer_shape"`
	ScenarioIDs *[]string             `json:"scenario_ids"`
	Needs       *[]RequirementItemRef `json:"needs"`
	// FollowConcepts set to true gives a question's needs back to its concepts.
	FollowConcepts *bool   `json:"follow_concepts"`
	Statement      *string `json:"statement"`
}

// RequirementMapping sets the drafted item a concept is; nil clears it.
type RequirementMapping struct {
	UpdateTime int64               `json:"update_time"`
	MapsTo     *RequirementItemRef `json:"maps_to"`
}

// RequirementResolve resolves a conflict: the side chosen, or a conclusion of its own, with a
// reason. Deferred hands it to the business to confirm instead, with a reason; it stays unresolved.
type RequirementResolve struct {
	UpdateTime int64  `json:"update_time"`
	DocumentID string `json:"document_id"`
	Text       string `json:"text"`
	Reason     string `json:"reason"`
	Deferred   bool   `json:"deferred"`
}

// RequirementDecide chooses an option of a modelling choice, with what it rests on.
type RequirementDecide struct {
	UpdateTime int64  `json:"update_time"`
	OptionID   string `json:"option_id"`
	Basis      string `json:"basis"`
}

// RequirementView is a specification as the client reads it, with what is computed from it.
type RequirementView struct {
	*Requirement
	// Stale is set when the documents or catalogs changed since the specification was drafted.
	Stale bool `json:"stale"`
	// UnresolvedConflicts counts the conflicts without a resolution, deferred ones included.
	UnresolvedConflicts int `json:"unresolved_conflicts"`
	// UndecidedChoices counts the modelling choices no one made; their recommended options hold.
	UndecidedChoices int `json:"undecided_choices"`
}

// ScenarioQuestions are a project's scenario questions as publishing and the regression after a
// release read them.
type ScenarioQuestions struct {
	// SpecVersion is n of the rq-n they come from; 0 when the project has no specification.
	SpecVersion int `json:"spec_version"`
	// UpdateTime is the specification's: it moves whenever the questions may have changed.
	UpdateTime int64              `json:"update_time"`
	Questions  []ScenarioQuestion `json:"questions"`
}

type ScenarioQuestion struct {
	ID    string               `json:"id"`
	Text  string               `json:"text"`
	Layer string               `json:"layer"`
	Needs []RequirementItemRef `json:"needs"`
	// MissingItems are the needs the current draft no longer has.
	MissingItems []RequirementItemRef `json:"missing_items"`
	// Tests are the test questions whose data check passed.
	Tests []ScenarioTest `json:"tests"`
}

type ScenarioTest struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// Statuses of a question's test questions and guidance as stored.
const (
	GUIDANCE_STATUS_GENERATING = "generating"
	GUIDANCE_STATUS_READY      = "ready"
	GUIDANCE_STATUS_FAILED     = "failed"
)

// What a test question expects of the data: the record exists, does not, or two of its slots name
// one record (an identifier and another way to name it).
const (
	TEST_EXPECT_EXISTS      = "exists"
	TEST_EXPECT_NOT_EXISTS  = "not_exists"
	TEST_EXPECT_SAME_RECORD = "same_record"
)

// Outcomes of checking a test question against the data. Only a passed one is asked after a
// release.
const (
	TEST_CHECK_PASSED      = "passed"
	TEST_CHECK_FAILED      = "failed"
	TEST_CHECK_NOT_CHECKED = "not_checked"
)

// QuestionGuidance is one row of t_bkn_project_question_guidance: the test questions and
// development guidance of a scenario question, for the specification version and question text
// they were generated for.
type QuestionGuidance struct {
	ProjectID   string `json:"project_id"`
	QuestionID  string `json:"question_id"`
	SpecVersion int    `json:"spec_version"`
	Question    string `json:"question"`
	Status      string `json:"status"`
	// Tests are the test questions, each with its data check.
	Tests []*QuestionTest `json:"tests"`
	// Guidance says how to answer the question on the network, as Markdown.
	Guidance string `json:"guidance"`
	// OntologySignature is the SHA-256 of the drafted items the guidance rests on.
	OntologySignature string `json:"ontology_signature"`
	// OutOfScope are further questions this one leads to that need more than it does.
	OutOfScope []string `json:"out_of_scope"`
	// NoTestsReason is why the Agent wrote no test questions, as it said, in the project's language.
	NoTestsReason string `json:"no_tests_reason"`
	// LeftOutTests counts the test questions the Agent wrote that the server left out: without
	// text, asking about a value they do not say, or past the most kept.
	LeftOutTests int `json:"left_out_tests"`
	// DeletedTests counts the test questions people deleted.
	DeletedTests int `json:"deleted_tests"`
	// Error is why generating failed, as a code for the client to word.
	Error       string      `json:"error,omitempty"`
	RunID       string      `json:"-"`
	AgentTaskID string      `json:"-"`
	Runner      AccountInfo `json:"runner"`
	CreateTime  int64       `json:"create_time"`
	UpdateTime  int64       `json:"update_time"`
}

// QuestionGuidanceView is a question's guidance as the client reads it. Current is false when the
// specification was drafted again or the question reworded since it was generated: it no longer
// applies, and the regression does not ask its test questions.
type QuestionGuidanceView struct {
	*QuestionGuidance
	Current bool `json:"current"`
}

// QuestionTest is a scenario question asked with concrete values. Kind is paraphrase, boundary,
// not_exist or variant; it says how the Agent meant it. Expect says what the data must show.
type QuestionTest struct {
	ID     string             `json:"id"`
	Text   string             `json:"text"`
	Kind   string             `json:"kind"`
	Slots  []QuestionTestSlot `json:"slots"`
	Expect string             `json:"expect"`
	Edited bool               `json:"edited"`
	Check  QuestionTestCheck  `json:"check"`
}

// RequirementTestEdit changes the fields it sets of a test question. UpdateTime is the guidance's
// as read.
type RequirementTestEdit struct {
	UpdateTime int64               `json:"update_time"`
	Text       *string             `json:"text"`
	Slots      *[]QuestionTestSlot `json:"slots"`
	Expect     *string             `json:"expect"`
}

// QuestionTestSlot is one value a test question uses: the concept it belongs to, the property of
// the concept's drafted object type it is a value of, and the value.
type QuestionTestSlot struct {
	ConceptID string `json:"concept_id"`
	Property  string `json:"property"`
	Value     string `json:"value"`
}

// QuestionTestCheck is what checking a test question against the data found. Reason is a code
// when it failed or could not be checked.
type QuestionTestCheck struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

//go:generate mockgen -source ../interfaces/requirement.go -destination ../interfaces/mock/mock_requirement.go
type RequirementAccess interface {
	// ListDocuments returns the project's documents oldest first, without content or blocks.
	ListDocuments(ctx context.Context, projectID string) ([]*RequirementDocument, error)
	// GetDocuments returns the project's documents oldest first, with content and blocks.
	GetDocuments(ctx context.Context, projectID string) ([]*RequirementDocument, error)
	// GetDocument returns one document with content and blocks, and false when the project has
	// no such document.
	GetDocument(ctx context.Context, projectID string, documentID string) (*RequirementDocument, bool, error)
	// CreateDocument stores doc and reports true, or reports false with the id of the document of
	// the project that already has the same content.
	CreateDocument(ctx context.Context, doc *RequirementDocument) (bool, string, error)
	// SetDocumentType sets a document's type; it reports false when the project has no such
	// document.
	SetDocumentType(ctx context.Context, projectID string, documentID string, docType string,
		updateTime int64) (bool, error)
	// SuggestDocumentType types a document as drafting suggests, only while it is still typed
	// other; it reports whether it did, and false means a person typed it meanwhile.
	SuggestDocumentType(ctx context.Context, projectID string, documentID string, docType string,
		updateTime int64) (bool, error)
	// DeleteDocument removes a document; it reports false when the project has no such document.
	DeleteDocument(ctx context.Context, projectID string, documentID string) (bool, error)

	// GetRequirement returns the project's specification, and false when it has none.
	GetRequirement(ctx context.Context, projectID string) (*Requirement, bool, error)
	// StartDrafting writes req, in the drafting state, as the project's specification unless the
	// project is drafting already; it reports whether it did. A specification drafting since
	// before staleBefore is taken over. One with decisions is replaced only when
	// discardDecisions is set; otherwise it returns ErrRequirementHasDecisions.
	StartDrafting(ctx context.Context, req *Requirement, staleBefore int64, discardDecisions bool) (bool, error)
	// UpdateRequirement writes the spec and update time of a ready specification whose update
	// time is still readUpdateTime; false means someone changed it meanwhile.
	UpdateRequirement(ctx context.Context, req *Requirement, readUpdateTime int64) (bool, error)
	// FinishDrafting records how the drafting run req.RunID ended; it reports false when another
	// run took the specification over.
	FinishDrafting(ctx context.Context, req *Requirement) (bool, error)
	// FailDrafting marks every specification still drafting as failed with reason; drafting runs
	// in the service process, so ones left drafting after a restart will never finish.
	FailDrafting(ctx context.Context, reason string, updateTime int64) (int64, error)

	// GetGuidance returns a question's test questions and guidance, and false when it has none.
	GetGuidance(ctx context.Context, projectID string, questionID string) (*QuestionGuidance, bool, error)
	// ListGuidance returns the test questions and guidance of every question of the project.
	ListGuidance(ctx context.Context, projectID string) ([]*QuestionGuidance, error)
	// StartGenerating writes g, generating, as the question's guidance unless it is generating
	// already; one generating since before staleBefore is taken over. It reports whether it did.
	StartGenerating(ctx context.Context, g *QuestionGuidance, staleBefore int64) (bool, error)
	// UpdateGuidance writes the tests, deleted count and update time of ready guidance whose
	// update time is still readUpdateTime; false means someone changed it meanwhile.
	UpdateGuidance(ctx context.Context, g *QuestionGuidance, readUpdateTime int64) (bool, error)
	// FinishGenerating records how the generating run g.RunID ended; false when another run took
	// the guidance over.
	FinishGenerating(ctx context.Context, g *QuestionGuidance) (bool, error)
	// FailGenerating marks all guidance still generating as failed with reason, as FailDrafting.
	FailGenerating(ctx context.Context, reason string, updateTime int64) (int64, error)
}

// Outcomes of uploading one file.
const (
	REQUIREMENT_UPLOAD_CREATED   = "created"
	REQUIREMENT_UPLOAD_DUPLICATE = "duplicate"
	REQUIREMENT_UPLOAD_REJECTED  = "rejected"
)

// RequirementUpload is one uploaded file: its name and bytes.
type RequirementUpload struct {
	Name    string
	Content []byte
}

// RequirementUploadResult is what became of one uploaded file. DocumentID is the document
// created, or the one that already has the same content; Code is the error code a rejected file
// was refused with.
type RequirementUploadResult struct {
	Name       string `json:"name"`
	Outcome    string `json:"outcome"`
	DocumentID string `json:"document_id,omitempty"`
	Code       string `json:"code,omitempty"`
}

// RequirementService serves a project's requirement documents and specification. token is the
// caller's Authorization header: the project must be visible to the caller. Only the project's
// owner changes them, and not while the project is archived.
type RequirementService interface {
	// UploadDocuments stores Markdown and plain-text files as the project's requirement
	// documents and reports each file's outcome; a refused file does not stop the others.
	UploadDocuments(ctx context.Context, token string, projectID string, files []RequirementUpload,
		caller AccountInfo) ([]RequirementUploadResult, error)
	// ListDocuments lists the project's documents oldest first, without their content.
	ListDocuments(ctx context.Context, token string, projectID string) ([]*RequirementDocument, error)
	// GetDocument returns one document with its content and blocks.
	GetDocument(ctx context.Context, token string, projectID string, documentID string) (*RequirementDocument, error)
	// SetDocumentType sets what kind of document it is, which decides its authority in conflicts.
	SetDocumentType(ctx context.Context, token string, projectID string, documentID string, docType string,
		caller AccountInfo) (*RequirementDocument, error)
	// DeleteDocument removes a document.
	DeleteDocument(ctx context.Context, token string, projectID string, documentID string, caller AccountInfo) error

	// GenerateRequirement drafts the project's specification from its documents in the
	// background and returns it drafting. A specification with decisions is drafted again only
	// when discardDecisions is set.
	GenerateRequirement(ctx context.Context, token string, projectID string, caller AccountInfo,
		discardDecisions bool) (*Requirement, error)
	// GetRequirement returns the project's specification with what is computed from it.
	GetRequirement(ctx context.Context, token string, projectID string) (*RequirementView, error)
	// FailInterrupted fails the specifications a previous run of the service left drafting.
	FailInterrupted(ctx context.Context) error

	// AddItem adds an item a person writes to the specification; EditItem changes one and
	// DeleteItem removes one. kind is scenarios, concepts, questions or rules. Each returns the
	// specification as it is after the change.
	AddItem(ctx context.Context, token string, projectID string, kind string, edit RequirementItemEdit,
		caller AccountInfo) (*Requirement, error)
	EditItem(ctx context.Context, token string, projectID string, kind string, itemID string,
		edit RequirementItemEdit, caller AccountInfo) (*Requirement, error)
	DeleteItem(ctx context.Context, token string, projectID string, kind string, itemID string,
		updateTime int64, caller AccountInfo) (*Requirement, error)
	// MapConcept sets or clears the drafted item a concept is; the needs of the questions that
	// follow their concepts follow.
	MapConcept(ctx context.Context, token string, projectID string, conceptID string, mapping RequirementMapping,
		caller AccountInfo) (*Requirement, error)
	// ResolveConflict records the owner's resolution of a conflict between documents, or that it
	// was handed to the business to confirm.
	ResolveConflict(ctx context.Context, token string, projectID string, conflictID string,
		resolve RequirementResolve, caller AccountInfo) (*Requirement, error)
	// DecideChoice records the option chosen for a modelling choice.
	DecideChoice(ctx context.Context, token string, projectID string, choiceID string, decide RequirementDecide,
		caller AccountInfo) (*Requirement, error)

	// GenerateGuidance generates a scenario question's test questions and development guidance in
	// the background and returns them generating.
	GenerateGuidance(ctx context.Context, token string, projectID string, questionID string,
		caller AccountInfo) (*QuestionGuidance, error)
	// GetGuidance returns a scenario question's test questions and guidance with whether they
	// still apply to the question as it is.
	GetGuidance(ctx context.Context, token string, projectID string, questionID string) (*QuestionGuidanceView, error)

	// EditTest changes a test question a person reviewed and checks it against the data again;
	// DeleteTest removes one, so the regression no longer asks it. Each returns the guidance after.
	EditTest(ctx context.Context, token string, projectID string, questionID string, testID string,
		edit RequirementTestEdit, caller AccountInfo) (*QuestionGuidance, error)
	DeleteTest(ctx context.Context, token string, projectID string, questionID string, testID string,
		updateTime int64, caller AccountInfo) (*QuestionGuidance, error)

	// ScenarioQuestions returns the project's scenario questions with their needs, for
	// publishing and the regression after a release, which check visibility themselves. A
	// project without a ready specification has none; that is no error.
	ScenarioQuestions(ctx context.Context, projectID string) (*ScenarioQuestions, error)
}
