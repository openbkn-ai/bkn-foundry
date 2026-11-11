// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package parsers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
)

// A Cypher function is a function tool whose code is one read-only Cypher
// statement. Its $name placeholders are its inputs, and it runs in bkn-backend
// rather than in the sandbox (see logics/toolbox/cypher_function.go).
//
// Everything checked here is static. Whether the statement compiles, reads only,
// names object types and properties that exist, and is readable by the author is
// for bkn-backend to say, and the toolbox service asks it with explain before
// saving.

// CypherParameterNames returns the distinct parameter names a statement uses,
// in order of first use.
//
// It agrees with bkn-backend's grammar on what a parameter is: $ followed by a
// name or a backtick-escaped name, never inside a string literal, a comment or
// an escaped identifier. The positional form ($0) is refused, as bkn-backend
// refuses it: parameters travel by name.
func CypherParameterNames(query string) ([]string, error) {
	runes := []rune(query)
	seen := map[string]bool{}
	names := []string{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; {
		case r == '/' && i+1 < len(runes) && runes[i+1] == '/':
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
		case r == '/' && i+1 < len(runes) && runes[i+1] == '*':
			i += 2
			for i+1 < len(runes) && (runes[i] != '*' || runes[i+1] != '/') {
				i++
			}
			i++ // onto the closing slash, or past the end of an unterminated comment
		case r == '\'' || r == '"':
			i = skipQuoted(runes, i, r)
		case r == '`':
			_, i = readEscapedName(runes, i)
		case r == '$':
			if i+1 >= len(runes) {
				continue
			}
			next := runes[i+1]
			switch {
			case next == '`':
				name, end := readEscapedName(runes, i+1)
				if name == "" {
					return nil, fmt.Errorf("a parameter with an empty name ($``) names nothing")
				}
				add(name)
				i = end
			case unicode.IsDigit(next):
				return nil, fmt.Errorf("positional parameters such as $%c are not supported; name the parameter, as in $value", next)
			case isCypherNameStart(next):
				j := i + 1
				for j < len(runes) && isCypherNamePart(runes[j]) {
					j++
				}
				add(string(runes[i+1 : j]))
				i = j - 1
			}
		}
	}
	return names, nil
}

// skipQuoted returns the index of the quote that closes the literal opened at start.
func skipQuoted(runes []rune, start int, quote rune) int {
	for i := start + 1; i < len(runes); i++ {
		switch runes[i] {
		case '\\':
			i++
		case quote:
			return i
		}
	}
	return len(runes)
}

// readEscapedName reads a backtick-escaped name opened at start, where a
// literal backtick is written doubled, and returns it with the index of the
// closing backtick.
func readEscapedName(runes []rune, start int) (string, int) {
	var b strings.Builder
	for i := start + 1; i < len(runes); i++ {
		if runes[i] != '`' {
			b.WriteRune(runes[i])
			continue
		}
		if i+1 < len(runes) && runes[i+1] == '`' {
			b.WriteRune('`')
			i++
			continue
		}
		return b.String(), i
	}
	return b.String(), len(runes)
}

func isCypherNameStart(r rune) bool { return r == '_' || unicode.IsLetter(r) }

func isCypherNamePart(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// cypherFunctionInput reports whether a function input is a Cypher function.
func cypherFunctionInput(inputValue any) (*interfaces.FunctionInput, bool) {
	input, ok := inputValue.(*interfaces.FunctionInput)
	if !ok || input == nil || input.ScriptType != interfaces.ScriptTypeCypher {
		return nil, false
	}
	return input, true
}

// cypherScalarTypes are the parameter types bkn-backend can bind.
var cypherScalarTypes = map[interfaces.ParameterType]bool{
	interfaces.ParameterTypeString:  true,
	interfaces.ParameterTypeNumber:  true,
	interfaces.ParameterTypeBoolean: true,
}

// validateCypherFunction checks a Cypher function without asking bkn-backend.
func (p *pythonFunctionParser) validateCypherFunction(ctx context.Context, input *interfaces.FunctionInput) error {
	if err := p.Validator.ValidatorStruct(ctx, input); err != nil {
		return err
	}
	if strings.TrimSpace(input.Code) == "" {
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest, "cypher function statement is empty")
	}
	if strings.TrimSpace(input.KnID) == "" {
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest, "cypher function needs kn_id: the knowledge network the statement reads")
	}
	switch input.ResultShape {
	case interfaces.CypherResultShapeValue, interfaces.CypherResultShapeTable:
	default:
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest,
			fmt.Sprintf("cypher function result_shape %q is invalid, must be value or table", input.ResultShape))
	}
	if len(input.Dependencies) > 0 || strings.TrimSpace(input.DependenciesURL) != "" {
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest, "a cypher function has no dependencies")
	}
	if input.Inputs == nil {
		input.Inputs = make([]*interfaces.ParameterDef, 0)
	}
	declared := map[string]bool{}
	for _, param := range input.Inputs {
		if err := validateCypherParameter(ctx, param); err != nil {
			return err
		}
		if declared[param.Name] {
			return errors.DefaultHTTPError(ctx, http.StatusBadRequest,
				fmt.Sprintf("input parameter %q is declared more than once", param.Name))
		}
		declared[param.Name] = true
	}
	used, err := CypherParameterNames(input.Code)
	if err != nil {
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
	}
	if err = matchCypherParameters(ctx, used, declared); err != nil {
		return err
	}
	return validateParameterDescriptions(ctx, input)
}

// validateCypherParameter allows what bkn-backend can bind: a scalar, or a list
// of one scalar type for IN $name. A parameter with no type is a string.
func validateCypherParameter(ctx context.Context, param *interfaces.ParameterDef) error {
	if param == nil || strings.TrimSpace(param.Name) == "" {
		return errors.DefaultHTTPError(ctx, http.StatusBadRequest, "input parameter has no name")
	}
	if param.Type == "" {
		param.Type = interfaces.ParameterTypeString
	}
	if cypherScalarTypes[param.Type] {
		if len(param.SubParameters) > 0 {
			return errors.DefaultHTTPError(ctx, http.StatusBadRequest,
				fmt.Sprintf("input parameter %q is a %s and has no sub_parameters", param.Name, param.Type))
		}
		return nil
	}
	if param.Type == interfaces.ParameterTypeArray && len(param.SubParameters) == 1 {
		item := param.SubParameters[0]
		if item != nil && cypherScalarTypes[item.Type] && len(item.SubParameters) == 0 {
			return nil
		}
	}
	return errors.DefaultHTTPError(ctx, http.StatusBadRequest,
		fmt.Sprintf("input parameter %q of a cypher function must be a string, a number, a boolean, "+
			"or an array of one of those for IN $%s", param.Name, param.Name))
}

// matchCypherParameters requires the statement's $names and the declared inputs
// to be the same set. bkn-backend ignores a supplied parameter the statement
// never uses, so a stale input would otherwise sit in the tool's schema forever,
// and an agent would fill it in for nothing.
func matchCypherParameters(ctx context.Context, used []string, declared map[string]bool) error {
	var missing, extra []string
	usedSet := map[string]bool{}
	for _, name := range used {
		usedSet[name] = true
		if !declared[name] {
			missing = append(missing, "$"+name)
		}
	}
	for name := range declared {
		if !usedSet[name] {
			extra = append(extra, name)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return nil
	}
	sort.Strings(extra)
	parts := []string{}
	if len(missing) > 0 {
		parts = append(parts, "the statement uses "+strings.Join(missing, ", ")+" with no input parameter")
	}
	if len(extra) > 0 {
		parts = append(parts, "input parameters "+strings.Join(extra, ", ")+" are not used by the statement")
	}
	return errors.DefaultHTTPError(ctx, http.StatusBadRequest,
		"cypher function parameters do not match the statement: "+strings.Join(parts, "; "))
}

// parseCypherFunction converts a checked Cypher function input into metadata.
func (p *pythonFunctionParser) parseCypherFunction(ctx context.Context, input *interfaces.FunctionInput) ([]interfaces.IMetadataDB, error) {
	if err := p.validateCypherFunction(ctx, input); err != nil {
		return nil, err
	}
	pathItem := convertCypherToPathItemContent(input)
	desc := pathItem.Description
	if desc == "" {
		desc = pathItem.Summary
	}
	return []interfaces.IMetadataDB{&model.FunctionMetadataDB{
		ScriptType:  model.ScriptTypeCypher,
		Code:        input.Code,
		KnID:        strings.TrimSpace(input.KnID),
		ResultShape: string(input.ResultShape),
		Summary:     pathItem.Summary,
		Description: desc,
		Path:        pathItem.Path,
		ServerURL:   pathItem.ServerURL,
		Method:      pathItem.Method,
		APISpec:     pathItem.APISpec.ToJSON(),
	}}, nil
}

// convertCypherToPathItemContent builds the contract a caller sees: the inputs
// as the request body, the result shape as the response. The address is the
// same function-exec address a Python function carries, so every place that
// lists or exports functions treats both alike; execution never calls it.
func convertCypherToPathItemContent(input *interfaces.FunctionInput) *interfaces.PathItemContent {
	result := &interfaces.PathItemContent{
		Path:        interfaces.GetAOIFuncExecPath(),
		Method:      http.MethodPost,
		ServerURL:   interfaces.AOIServerURL,
		Summary:     input.Name,
		Description: input.Description,
		APISpec:     &interfaces.APISpec{},
	}
	result.APISpec.Parameters = createParameter()
	result.APISpec.RequestBody = createRequestBody(input.Inputs)
	result.APISpec.Responses = createCypherResponses(input.ResultShape)
	return result
}

func createCypherResponses(shape interfaces.CypherResultShape) []*interfaces.Response {
	body := openapi3.NewObjectSchema()
	if shape == interfaces.CypherResultShapeValue {
		body.Description = "The single value the statement returns; null when it matches nothing"
		body.Properties["value"] = openapi3.NewSchemaRef("", &openapi3.Schema{
			Description: "A string, a number, a boolean or null. A decimal property and a sum() over one " +
				"come back as a string such as \"13833141.000000\", keeping their precision; " +
				"a computed value such as a ratio comes back as a number",
			Nullable: true,
		})
		body.Required = []string{"value"}
	} else {
		body.Description = "The rows the statement returns"
		columns := openapi3.NewArraySchema().WithItems(openapi3.NewObjectSchema())
		columns.Description = "Returned columns: name and type"
		entries := openapi3.NewArraySchema().WithItems(openapi3.NewObjectSchema())
		entries.Description = "One object per row, keyed by column name. A decimal value comes back as " +
			"a string such as \"13833141.000000\", keeping its precision; a computed value such as a " +
			"ratio comes back as a number"
		limited := openapi3.NewBoolSchema()
		limited.Description = "True when the statement wrote no LIMIT and the default cut the rows short"
		body.Properties["columns"] = openapi3.NewSchemaRef("", columns)
		body.Properties["entries"] = openapi3.NewSchemaRef("", entries)
		body.Properties["limited"] = openapi3.NewSchemaRef("", limited)
		body.Required = []string{"columns", "entries"}
	}
	errSchema := openapi3.NewObjectSchema()
	errSchema.Properties["error_code"] = openapi3.NewSchemaRef("", openapi3.NewStringSchema())
	errSchema.Properties["description"] = openapi3.NewSchemaRef("", openapi3.NewStringSchema())
	errSchema.Properties["error_details"] = openapi3.NewSchemaRef("", &openapi3.Schema{})
	return []*interfaces.Response{
		{StatusCode: "200", Description: "Success", Content: openapi3.NewContentWithJSONSchema(body)},
		{StatusCode: "400", Description: "Invalid parameters, or a result that does not fit the shape",
			Content: openapi3.NewContentWithJSONSchema(errSchema)},
		{StatusCode: "403", Description: "The caller cannot read what the statement reads",
			Content: openapi3.NewContentWithJSONSchema(errSchema)},
	}
}
