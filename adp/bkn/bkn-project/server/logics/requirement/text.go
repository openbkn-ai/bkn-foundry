// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package requirement

import (
	"regexp"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/comm-go/i18n"

	"github.com/openbkn-ai/bkn-foundry/adp/bkn/bkn-project/server/interfaces"
)

// A question's text is what people read; its template keeps the {name} slots. An Agent sometimes
// writes a slot into the text as well: it is said in business words before the question is kept,
// a given record of its concept, or a given date for a moment.

var (
	textSlot = regexp.MustCompile(`\s*\{\s*([A-Za-z_][A-Za-z0-9_.\- ]*?)\s*\}\s*`)
	// timeSlot is a slot no input names that still reads as a moment, such as as_of or ship_date.
	timeSlot = regexp.MustCompile(`(?i)(^|_)(as_of|date|time|day|moment|period|month|year|since|until)(_|$)`)
	// spaceBeforeMark is a space left before punctuation where an English slot stood.
	spaceBeforeMark = regexp.MustCompile(`\s+([?,.!;:])`)
)

// plainText is the question's text with each slot said in business words, in the language given.
func plainText(q *interfaces.RequirementQuestion, spec *interfaces.RequirementSpec, draft *interfaces.Draft,
	language string) string {
	zh := language == interfaces.NAMING_LANGUAGE_ZH_CN
	text := q.Text
	for {
		at := textSlot.FindStringSubmatchIndex(text)
		if at == nil {
			return strings.TrimSpace(text)
		}
		label, moment := slotLabel(text[at[2]:at[3]], q, spec, draft)
		before, after := text[:at[0]], text[at[1]:]
		// A label the text says right before the slot is the one the slot gives: said once.
		if label != "" && strings.HasSuffix(strings.ToLower(strings.TrimSpace(before)), strings.ToLower(label)) {
			before = strings.TrimSpace(before)
			before = before[:len(before)-len(label)]
		}
		phrase := givenPhrase(label, moment, language)
		if !zh {
			phrase = " " + phrase + " "
			if strings.TrimSpace(before) == "" {
				phrase = strings.ToUpper(phrase[1:2]) + phrase[2:]
			}
		}
		text = strings.TrimRight(before, " ") + phrase + strings.TrimLeft(after, " ")
		if !zh {
			text = spaceBeforeMark.ReplaceAllString(text, "$1")
		}
	}
}

// slotLabel is what a slot names, and whether it is a moment: its input's concept term, or for a
// moment the display name of the property holding it.
func slotLabel(name string, q *interfaces.RequirementQuestion, spec *interfaces.RequirementSpec,
	draft *interfaces.Draft) (string, bool) {
	if q.Inputs != nil {
		for _, in := range *q.Inputs {
			if in.Name != name {
				continue
			}
			moment := in.Role == interfaces.REQUIREMENT_INPUT_REFERENCE_TIME
			var concept *interfaces.RequirementConcept
			for _, c := range spec.Concepts {
				if c.ID == in.ConceptID {
					concept = c
				}
			}
			if moment {
				return propertyDisplayName(concept, in.Property, draft), true
			}
			if concept != nil {
				return concept.Term, false
			}
			return "", false
		}
	}
	return "", timeSlot.MatchString(name)
}

// propertyDisplayName is the display name of the property holding a moment, empty when unknown or
// when it is the property's own name, which is no business word.
func propertyDisplayName(concept *interfaces.RequirementConcept, property string, draft *interfaces.Draft) string {
	if concept == nil || concept.MapsTo == nil || draft == nil {
		return ""
	}
	for _, ot := range draft.Items.ObjectTypes {
		if ot.ID != concept.MapsTo.ID {
			continue
		}
		for _, p := range ot.Properties {
			if p.Name == property && p.DisplayName != p.Name {
				return p.DisplayName
			}
		}
	}
	return ""
}

// Localized words a slot is said in: a given record of a concept, a given date, a given value.
const (
	messageGivenRecord = "BknProject.Requirement.GivenRecord"
	messageGivenDate   = "BknProject.Requirement.GivenDate"
	messageGivenValue  = "BknProject.Requirement.GivenValue"
)

func givenPhrase(label string, moment bool, language string) string {
	key := messageGivenValue
	switch {
	case label != "":
		key = messageGivenRecord
	case moment:
		key = messageGivenDate
	}
	phrase, err := i18n.Render(i18n.Language(language), key, map[string]any{"label": label})
	if err != nil {
		return label
	}
	return phrase
}
