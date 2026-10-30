// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/openbkn-ai/bkn-foundry/comm-go/httperrors"
	"github.com/openbkn-ai/bkn-foundry/comm-go/logger"
	"github.com/openbkn-ai/bkn-foundry/comm-go/otel/oteltrace"
	"go.opentelemetry.io/otel/codes"

	perrors "github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A scenario question is a template: asked as it is, an answer can only ask back which record is
// meant. Its test questions ask it with concrete values, which are checked against the data
// before the regression after a release asks them. bkn-agent writes them, with the development
// guidance, from the question and the structure of the drafted items it rests on; no data goes to
// it.

// guidanceLease is how long a generating run holds the guidance before another may take it over;
// finishMargin is kept of it to record the outcome.
const (
	guidanceLease = agentDeadline + time.Minute
	finishMargin  = 30 * time.Second
)

// maxTests bounds the test questions of one scenario question.
const maxTests = 20

// maxNoTestsReason bounds, in characters, the reason kept for writing no test questions.
const maxNoTestsReason = 1000

// checkPending is the check reason of a test question not checked against the data yet.
const checkPending = "pending"

var testKinds = []string{"paraphrase", "boundary", interfaces.TEST_KIND_NOT_EXIST, "variant"}

// kindExpects is what a test of each kind expects of the data, whatever the Agent wrote: a
// paraphrase or a boundary asks about a record that exists, a variant names one record twice.
var kindExpects = map[string]string{
	"paraphrase": interfaces.TEST_EXPECT_EXISTS, "boundary": interfaces.TEST_EXPECT_EXISTS,
	interfaces.TEST_KIND_NOT_EXIST: interfaces.TEST_EXPECT_NOT_EXISTS, "variant": interfaces.TEST_EXPECT_SAME_RECORD,
}

// guidanceOutput is what the Agent returns in pass guidance.
type guidanceOutput struct {
	Tests         []agentTest `json:"tests"`
	NoTestsReason string      `json:"no_tests_reason"`
	OutOfScope    []string    `json:"out_of_scope"`
	Guidance      string      `json:"guidance"`
}

type agentTest struct {
	Text   string      `json:"text"`
	Kind   string      `json:"kind"`
	Slots  []agentSlot `json:"slots"`
	Expect string      `json:"expect"`
}

type agentSlot struct {
	ConceptID string `json:"concept_id"`
	Property  string `json:"property"`
	Value     string `json:"value"`
}

// guidanceResponseFormat is the JSON Schema of pass guidance.
func guidanceResponseFormat() map[string]any {
	return schemaObject(map[string]any{
		"tests": schemaArray(schemaObject(map[string]any{
			"text": schemaString(),
			"kind": schemaEnum(testKinds...),
			"slots": schemaArray(schemaObject(map[string]any{
				"concept_id": schemaString(), "property": schemaString(), "value": schemaString(),
			}, "concept_id", "property", "value")),
			"expect": schemaEnum(interfaces.TEST_EXPECT_EXISTS, interfaces.TEST_EXPECT_NOT_EXISTS,
				interfaces.TEST_EXPECT_SAME_RECORD),
		}, "text", "kind", "slots", "expect")),
		// Described here too, for a prompt that does not name it yet.
		"no_tests_reason": map[string]any{"type": "string",
			"description": "When no test is written, why, in one sentence; otherwise empty."},
		"out_of_scope": schemaStrings(),
		"guidance":     schemaString(),
	}, "tests", "no_tests_reason", "out_of_scope", "guidance")
}

// guidanceMessage is the user message of pass guidance: the question, its scenarios, the rules,
// the concepts with the drafted items they map to, and those items' structure, as data.
type guidanceMessage struct {
	Question      guidanceQuestion      `json:"question"`
	Scenarios     []guidanceScenario    `json:"scenarios"`
	Rules         []guidanceRule        `json:"rules"`
	Concepts      []guidanceConcept     `json:"concepts"`
	ObjectTypes   []guidanceObjectType  `json:"object_types"`
	RelationTypes []messageRelationType `json:"relation_types"`
}

type guidanceQuestion struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	Type        string `json:"type"`
	Layer       string `json:"layer"`
	Template    string `json:"template,omitempty"`
	AnswerShape string `json:"answer_shape,omitempty"`
	// Inputs are what the tests' slots name; [] says the question has none to name.
	Inputs         *[]interfaces.RequirementInput `json:"inputs,omitempty"`
	RelativeWindow bool                           `json:"relative_window,omitempty"`
	// ServerTests says the server writes the template and not_exist tests, so the Agent writes neither.
	ServerTests bool `json:"server_tests,omitempty"`
}

type guidanceScenario struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Decision string `json:"decision,omitempty"`
}

type guidanceRule struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
}

type guidanceConcept struct {
	ID         string                         `json:"id"`
	Term       string                         `json:"term"`
	Category   string                         `json:"category"`
	Definition string                         `json:"definition,omitempty"`
	MapsTo     *interfaces.RequirementItemRef `json:"maps_to,omitempty"`
}

type guidanceObjectType struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Properties []messageProperty `json:"properties"`
}

type messageProperty struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Type        string `json:"type"`
}

func buildGuidanceMessage(language string, spec *interfaces.RequirementSpec, q *interfaces.RequirementQuestion,
	draft *interfaces.Draft) (string, error) {
	msg := guidanceMessage{
		Question: guidanceQuestion{ID: q.ID, Text: q.Text, Type: q.Type, Layer: q.Layer, Template: q.Template,
			AnswerShape: q.AnswerShape, Inputs: q.Inputs, RelativeWindow: q.RelativeWindow,
			ServerTests: len(serverTests(q, unmappedConcepts(spec))) > 0},
		Scenarios: []guidanceScenario{}, Rules: []guidanceRule{}, Concepts: []guidanceConcept{},
		ObjectTypes: []guidanceObjectType{}, RelationTypes: []messageRelationType{},
	}
	for _, s := range spec.Scenarios {
		if slices.Contains(q.ScenarioIDs, s.ID) {
			msg.Scenarios = append(msg.Scenarios, guidanceScenario{ID: s.ID, Name: s.Name, Decision: s.Decision})
		}
	}
	for _, r := range spec.Rules {
		msg.Rules = append(msg.Rules, guidanceRule{ID: r.ID, Statement: r.Statement})
	}
	// Every concept may name a value, so all go; the drafted items are those they map to and the
	// question needs.
	mapped := map[interfaces.RequirementItemRef]bool{}
	for _, c := range spec.Concepts {
		msg.Concepts = append(msg.Concepts, guidanceConcept{ID: c.ID, Term: c.Term, Category: c.Category,
			Definition: c.Definition, MapsTo: c.MapsTo})
		if c.MapsTo != nil {
			mapped[*c.MapsTo] = true
		}
	}
	// With the path that joins them in the draft, so the Agent sees the way between them.
	path, _ := questionPath(q, draft)
	for _, need := range append(append([]interfaces.RequirementItemRef{}, q.Needs...), path...) {
		mapped[need] = true
	}
	if draft != nil {
		for _, ot := range draft.Items.ObjectTypes {
			if !mapped[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_OBJECT_TYPE, ID: ot.ID}] {
				continue
			}
			props := []messageProperty{}
			for _, p := range ot.Properties {
				props = append(props, messageProperty{Name: p.Name, DisplayName: p.DisplayName, Type: p.Type})
			}
			msg.ObjectTypes = append(msg.ObjectTypes, guidanceObjectType{ID: ot.ID, Name: ot.Name, Properties: props})
		}
		for _, rt := range draft.Items.RelationTypes {
			if mapped[interfaces.RequirementItemRef{Kind: interfaces.REQUIREMENT_ITEM_RELATION_TYPE, ID: rt.ID}] {
				msg.RelationTypes = append(msg.RelationTypes, messageRelationType{ID: rt.ID, Name: rt.Name,
					Source: rt.Source, Target: rt.Target})
			}
		}
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return "", err
	}
	return "pass guidance\nlanguage: " + language + "\n" + string(body), nil
}

// slotPlaceholder stands in a test question's text for the value of its slot i, until the
// server fills it from the data.
func slotPlaceholder(i int) string {
	return fmt.Sprintf("{{slot %d}}", i+1)
}

// valueInDocuments tells whether a value is written in one of the documents, compared as quotes
// are: a value the documents do not say, the Agent made up.
func valueInDocuments(value string, texts []string) bool {
	v := normalize([]rune(value)).text
	if v == "" {
		return false
	}
	for _, text := range texts {
		if strings.Contains(text, v) {
			return true
		}
	}
	return false
}

// valueSpan is where a made-up value stands in a test's text, by byte offsets, and the slot it is.
type valueSpan struct {
	start, end, slot int
}

// findValue finds the value in the text as a whole word, not inside a longer word or number, nor
// where another slot's value stands. Text written without spaces between words, as Chinese is,
// bounds a value anywhere.
func findValue(text string, value string, taken []valueSpan) (valueSpan, bool) {
	if value == "" {
		return valueSpan{}, false
	}
	for from := 0; from <= len(text)-len(value); {
		i := strings.Index(text[from:], value)
		if i < 0 {
			break
		}
		start, end := from+i, from+i+len(value)
		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])
		free := !wordRune(before) && !wordRune(after)
		for _, sp := range taken {
			if start < sp.end && sp.start < end {
				free = false
			}
		}
		if free {
			return valueSpan{start: start, end: end}, true
		}
		_, size := utf8.DecodeRuneInString(text[start:])
		from = start + size
	}
	return valueSpan{}, false
}

func wordRune(r rune) bool {
	return r != utf8.RuneError && (unicode.IsDigit(r) || unicode.IsLetter(r) && !unicode.Is(unicode.Han, r))
}

// replaceSpans puts each slot's placeholder where its value stands, from the end so that the
// offsets before stay right.
func replaceSpans(text string, spans []valueSpan) string {
	slices.SortFunc(spans, func(a, b valueSpan) int { return b.start - a.start })
	for _, sp := range spans {
		text = text[:sp.start] + slotPlaceholder(sp.slot) + text[sp.end:]
	}
	return text
}

// buildTests gives the Agent's test questions the server's ids, at most maxTests of them, and
// counts those it leaves out: without text, asking about a made-up value they do not say as
// written, or without the place of a slot left empty. A value is kept only when a document says
// it; the others are left for the server to fill from the data. None is checked against the data
// yet. A question with inputs keeps only the tests that ask about them (see offInputs), after
// those the server writes from its template (see serverTests): then the Agent's not_exist ones,
// which the server's stands for, are left out too, and so are those with a slot of a concept that
// maps to no object type: no field holds its value. A test's kind decides what it expects.
func buildTests(out *guidanceOutput, docs []*interfaces.RequirementDocument,
	q *interfaces.RequirementQuestion, spec *interfaces.RequirementSpec) ([]*interfaces.QuestionTest, int) {
	concepts, unmapped := inputConcepts(q), unmappedConcepts(spec)
	texts := make([]string, 0, len(docs))
	for _, d := range docs {
		texts = append(texts, normalize([]rune(d.Content)).text)
	}
	tests, leftOut := serverTests(q, unmapped), 0
	for i, t := range tests {
		t.ID = fmt.Sprintf("t%d", i+1)
	}
	anchored := len(tests) > 0
	for _, at := range out.Tests {
		text := strings.TrimSpace(at.Text)
		if text == "" || len(tests) == maxTests || len(tests) > 0 && tests[0].ByServer && at.Kind == interfaces.TEST_KIND_NOT_EXIST {
			leftOut++
			continue
		}
		expect, known := kindExpects[at.Kind]
		if !known {
			expect = at.Expect
		}
		if expect != interfaces.TEST_EXPECT_NOT_EXISTS && expect != interfaces.TEST_EXPECT_SAME_RECORD {
			expect = interfaces.TEST_EXPECT_EXISTS
		}
		slots := make([]interfaces.QuestionTestSlot, 0, len(at.Slots))
		var spans []valueSpan
		placed := true
		for i, s := range at.Slots {
			value := strings.TrimSpace(s.Value)
			if value == "" && !strings.Contains(text, slotPlaceholder(i)) {
				// The value the server fills would stand nowhere in the question.
				placed = false
			}
			if !valueInDocuments(value, texts) {
				// The text asks about the made-up value: it gives way to the slot, which the
				// server's value from the data replaces. A text that does not say it as written
				// would go on asking about a value no one checks, so the test is left out.
				if value != "" {
					sp, ok := findValue(text, value, spans)
					sp.slot = i
					spans, placed = append(spans, sp), placed && ok
				}
				value = ""
			}
			slots = append(slots, interfaces.QuestionTestSlot{ConceptID: s.ConceptID, Property: s.Property, Value: value})
		}
		// One value names the same record only as an empty slot beside the server's template test,
		// filled from and checked against that test's record; otherwise it would only ever be said
		// not checkable.
		if !placed || expect == interfaces.TEST_EXPECT_SAME_RECORD && len(slots) < 2 &&
			!(anchored && len(slots) == 1 && slots[0].Value == "") ||
			(concepts != nil && (offInputs(slots, expect, concepts) ||
				slices.ContainsFunc(slots, func(s interfaces.QuestionTestSlot) bool { return unmapped[s.ConceptID] }))) {
			leftOut++
			continue
		}
		text = replaceSpans(text, spans)
		tests = append(tests, &interfaces.QuestionTest{
			ID: fmt.Sprintf("t%d", len(tests)+1), Text: text, Kind: at.Kind, Slots: slots, Expect: expect,
			Check: interfaces.QuestionTestCheck{Outcome: interfaces.TEST_CHECK_NOT_CHECKED, Reason: checkPending},
		})
	}
	return tests, leftOut
}

// clip keeps at most n characters of s.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// runAgent starts the requirements Agent as the caller and waits for its task up to the deadline,
// returning its output, or the failure code with why. onTask learns the task once it starts.
func (s *requirementService) runAgent(ctx context.Context, caller interfaces.AccountInfo, message string,
	format map[string]any, onTask func(string)) (string, string, error) {
	task, err := s.ba.RunTask(ctx, caller, &interfaces.BknAgentRunRequest{
		AgentID: requirementAgentID, Message: message, ResponseFormat: format,
	})
	if err != nil {
		return "", startFailure(err), err
	}
	onTask(task.TaskID)
	deadline := s.now() + agentDeadline.Milliseconds()
	for !task.Terminal() {
		if s.now() >= deadline {
			return "", failureAgentTimeout, fmt.Errorf("task %s did not finish", task.TaskID)
		}
		s.sleep(pollInterval)
		readCtx, cancel := context.WithTimeout(ctx, taskReadTimeout)
		read, err := s.ba.GetTask(readCtx, caller, task.TaskID)
		cancel()
		if err == nil {
			task = read
		}
	}
	if task.Status != interfaces.BKN_AGENT_TASK_SUCCEEDED {
		return "", failureAgentFailed, errors.New(task.FailureDetail)
	}
	return task.Output, "", nil
}

// readySpecOf returns the project's ready specification, refusing one drafting or missing.
func (s *requirementService) readySpecOf(ctx context.Context, projectID string) (*interfaces.Requirement, error) {
	req, ok, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	switch {
	case ok && req.Status == interfaces.REQUIREMENT_STATUS_DRAFTING:
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Drafting)
	case !ok || req.Status != interfaces.REQUIREMENT_STATUS_READY:
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_NotFound)
	}
	return req, nil
}

func questionOf(spec *interfaces.RequirementSpec, questionID string) *interfaces.RequirementQuestion {
	for _, q := range spec.Questions {
		if q.ID == questionID {
			return q
		}
	}
	return nil
}

func (s *requirementService) GenerateGuidance(ctx context.Context, token string, projectID string,
	questionID string, caller interfaces.AccountInfo) (*interfaces.QuestionGuidance, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GenerateQuestionGuidance")
	defer span.End()

	entry, err := s.ownedProject(ctx, token, projectID, caller)
	if err != nil {
		span.SetStatus(codes.Error, "Project not writable")
		return nil, err
	}
	req, err := s.readySpecOf(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Specification not ready")
		return nil, err
	}
	q := questionOf(&req.Spec, questionID)
	if q == nil {
		span.SetStatus(codes.Error, "Question not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_ItemNotFound)
	}
	draft, ok, err := s.da.GetDraft(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get draft failed")
		return nil, internalError(ctx, "Get draft failed", err)
	}
	if !ok {
		draft = nil
	}
	docs, err := s.ra.GetDocuments(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get documents failed")
		return nil, internalError(ctx, "Get documents failed", err)
	}

	now := s.now()
	g := &interfaces.QuestionGuidance{
		ProjectID: projectID, QuestionID: q.ID, SpecVersion: req.Version, Question: q.Text,
		Status: interfaces.GUIDANCE_STATUS_GENERATING, Tests: []*interfaces.QuestionTest{}, OutOfScope: []string{},
		RunID: s.newID(), Runner: caller, CreateTime: now, UpdateTime: now,
		// Of the draft generating reads, so that a later change to it shows.
		OntologySignature: ontologySignature(draft, q),
	}
	staleBefore := now - guidanceLease.Milliseconds()
	started, err := s.ra.StartGenerating(ctx, g, staleBefore)
	if err != nil {
		span.SetStatus(codes.Error, "Start generating failed")
		return nil, internalError(ctx, "Start generating failed", err)
	}
	if !started {
		span.SetStatus(codes.Error, "Generating")
		return nil, httperrors.NewHTTPError(ctx, http.StatusConflict, perrors.BknProject_Requirement_Generating)
	}
	spec, language := req.Spec, entry.NamingLanguage
	runCtx := context.WithoutCancel(ctx)
	s.run(func() { s.generate(runCtx, g, &spec, q, draft, docs, language, caller) })
	span.SetStatus(codes.Ok, "")
	return g, nil
}

// generate runs the Agent and records the test questions and guidance, or why it failed.
func (s *requirementService) generate(ctx context.Context, g *interfaces.QuestionGuidance,
	spec *interfaces.RequirementSpec, q *interfaces.RequirementQuestion, draft *interfaces.Draft,
	docs []*interfaces.RequirementDocument, language string, caller interfaces.AccountInfo) {
	finish := func(done *interfaces.QuestionGuidance) {
		done.UpdateTime = s.now()
		if _, err := s.ra.FinishGenerating(ctx, done); err != nil {
			logger.Errorf("Record guidance of question %s of project %s failed: %v", g.QuestionID, g.ProjectID, err)
		}
	}
	fail := func(failure string, err error) {
		logger.Errorf("Guidance of question %s of project %s failed (%s): %v", g.QuestionID, g.ProjectID, failure, err)
		done := *g
		done.Status, done.Error = interfaces.GUIDANCE_STATUS_FAILED, failure
		done.Tests, done.OutOfScope, done.Guidance = []*interfaces.QuestionTest{}, []string{}, ""
		finish(&done)
	}

	message, err := buildGuidanceMessage(language, spec, q, draft)
	if err != nil {
		fail(failureAgentUnavailable, err)
		return
	}
	output, failure, err := s.runAgent(ctx, caller, message, guidanceResponseFormat(),
		func(taskID string) { g.AgentTaskID = taskID })
	if failure != "" {
		fail(failure, err)
		return
	}
	var out guidanceOutput
	if err := json.Unmarshal([]byte(output), &out); err != nil {
		fail(failureOutputInvalid, err)
		return
	}
	done := *g
	done.Status, done.Guidance = interfaces.GUIDANCE_STATUS_READY, strings.TrimSpace(out.Guidance)
	done.Tests, done.LeftOutTests = buildTests(&out, docs, q, spec)
	// A reason for writing none applies only when none is kept.
	if len(done.Tests) == 0 {
		done.NoTestsReason = clip(strings.TrimSpace(out.NoTestsReason), maxNoTestsReason)
	}
	// Checked as the person who generated them, before anyone relies on them, and in time to
	// record the outcome before another run may take the guidance over.
	s.checkTests(ctx, caller, spec, draft, q, done.Tests, g.UpdateTime+guidanceLease.Milliseconds()-finishMargin.Milliseconds())
	done.OutOfScope = []string{}
	for _, o := range out.OutOfScope {
		if o = strings.TrimSpace(o); o != "" {
			done.OutOfScope = append(done.OutOfScope, o)
		}
	}
	finish(&done)
}

// guidanceCurrent tells whether guidance still applies to the question: generated for the same
// version and question text of the specification as drafted last, since drafting again may reuse both.
func guidanceCurrent(req *interfaces.Requirement, q *interfaces.RequirementQuestion, g *interfaces.QuestionGuidance) bool {
	return req.Version == g.SpecVersion && q.Text == g.Question && g.CreateTime >= req.CreateTime
}

// testsNote says what a question's tests show as a whole: inputs_untested when the question has
// inputs and no test names each of their concepts; none_checkable when it has some and each names
// a concept or property its items do not have. q is nil when the specification is not read.
func testsNote(tests []*interfaces.QuestionTest, q *interfaces.RequirementQuestion) string {
	if concepts := inputConcepts(q); len(concepts) > 0 && !slices.ContainsFunc(tests,
		func(t *interfaces.QuestionTest) bool { return namesAll(t.Slots, concepts) }) {
		return interfaces.TESTS_NOTE_INPUTS_UNTESTED
	}
	if len(tests) == 0 {
		return ""
	}
	for _, t := range tests {
		// Only a test not checked has these reasons.
		if t.Check.Reason != checkUnknownConcept && t.Check.Reason != checkUnknownProperty {
			return ""
		}
	}
	return interfaces.TESTS_NOTE_NONE_CHECKABLE
}

func (s *requirementService) GetGuidance(ctx context.Context, token string, projectID string,
	questionID string) (*interfaces.QuestionGuidanceView, error) {
	ctx, span := oteltrace.StartNamedInternalSpan(ctx, "GetQuestionGuidance")
	defer span.End()

	if _, err := s.ps.GetProject(ctx, token, projectID); err != nil {
		span.SetStatus(codes.Error, "Get project failed")
		return nil, err
	}
	g, ok, err := s.ra.GetGuidance(ctx, projectID, questionID)
	if err != nil {
		span.SetStatus(codes.Error, "Get guidance failed")
		return nil, internalError(ctx, "Get guidance failed", err)
	}
	if !ok {
		span.SetStatus(codes.Error, "Guidance not found")
		return nil, httperrors.NewHTTPError(ctx, http.StatusNotFound, perrors.BknProject_Requirement_TestNotFound)
	}
	req, found, err := s.ra.GetRequirement(ctx, projectID)
	if err != nil {
		span.SetStatus(codes.Error, "Get requirement failed")
		return nil, internalError(ctx, "Get requirement failed", err)
	}
	view := &interfaces.QuestionGuidanceView{QuestionGuidance: g, TestsNote: testsNote(g.Tests, nil)}
	if found && req.Status == interfaces.REQUIREMENT_STATUS_READY {
		q := questionOf(&req.Spec, questionID)
		view.TestsNote = testsNote(g.Tests, q)
		view.Current = q != nil && guidanceCurrent(req, q, g)
		if q != nil && g.OntologySignature != "" {
			draft, ok, err := s.da.GetDraft(ctx, projectID)
			if err != nil {
				span.SetStatus(codes.Error, "Get draft failed")
				return nil, internalError(ctx, "Get draft failed", err)
			}
			if ok {
				changed := ontologySignature(draft, q) != g.OntologySignature
				view.OntologyChanged = &changed
			}
		}
	}
	span.SetStatus(codes.Ok, "")
	return view, nil
}
