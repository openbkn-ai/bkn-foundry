// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

package toolbox

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	oerrors "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces/model"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/parsers"
	proxyexecution "github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/logics/proxy_execution"
)

// A Cypher function (#2670) is a function tool whose code is one read-only
// Cypher statement. It does not go to the function runtime or the sandbox: the
// toolbox service runs the statement in bkn-backend itself, as the account this
// call was authenticated and authorized as. A direct call (debug, agent, MCP)
// therefore reads with the caller's own row and column permissions; a logic
// property arrives from ontology-query as the knowledge network's proxy account
// and reads with that account's.
//
// Nothing here needs a token or a managed Interaction, which is why a Cypher
// logic property keeps working when capture is off.

// graphValueKinds are the value kinds bkn-backend gives a node, a relationship,
// a map or a list. Anything else is a scalar, whose kind is its type name.
var graphValueKinds = map[string]bool{"object": true, "relation": true, "map": true, "list": true}

// cypherColumn is one returned column, as bkn-backend describes it in a plan
// or a result.
type cypherColumn struct {
	Name string `json:"name"`
	Type *struct {
		Kind string `json:"kind"`
	} `json:"type,omitempty"`
}

type cypherExplainReply struct {
	Plan *struct {
		Columns []cypherColumn `json:"columns"`
	} `json:"plan"`
}

type cypherRunReply struct {
	Columns    []json.RawMessage `json:"columns"`
	Entries    []map[string]any  `json:"entries"`
	Limited    bool              `json:"limited,omitempty"`
	ValueTypes map[string]struct {
		Kind string `json:"kind"`
	} `json:"value_types,omitempty"`
}

// validateCypherFunctionMetadata asks bkn-backend, as the author, whether the
// statement can run: it compiles, reads only, names object types and properties
// that exist, and is readable by the author. explain plans without reading data,
// so this costs no query. A value function must also return one scalar column.
func (s *ToolServiceImpl) validateCypherFunctionMetadata(ctx context.Context, metadata interfaces.IMetadataDB) error {
	function, ok := metadata.(*model.FunctionMetadataDB)
	if !ok || !function.IsCypher() {
		return nil
	}
	if s.BknBackend == nil {
		return oerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "cypher functions are unavailable: bkn-backend is not configured")
	}
	inputs, _ := parsers.FunctionParamsFromAPISpec(function.APISpec)
	result, err := s.BknBackend.RunCypher(ctx, function.KnID, &interfaces.CypherQueryReq{
		Query:      function.Code,
		Parameters: cypherExplainParameters(inputs),
		Explain:    true,
	})
	if err != nil {
		return err
	}
	if !isSuccess(result.StatusCode) {
		return cypherRefusal(ctx, result)
	}
	if interfaces.CypherResultShape(function.ResultShape) != interfaces.CypherResultShapeValue {
		return nil
	}
	reply := cypherExplainReply{}
	if err = json.Unmarshal(result.Body, &reply); err != nil || reply.Plan == nil {
		// No plan to judge by; the run checks the shape instead.
		return nil
	}
	columns := reply.Plan.Columns
	if len(columns) != 1 {
		return oerrors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf(
			"a value cypher function must return exactly one column, this statement returns %d; "+
				"return one expression, or choose the table shape", len(columns)))
	}
	if columns[0].Type != nil && graphValueKinds[columns[0].Type.Kind] {
		return oerrors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf(
			"a value cypher function must return a string, a number or a boolean, column %q is of kind %s; "+
				"return a property or an aggregate such as count(), or choose the table shape",
			columns[0].Name, columns[0].Type.Kind))
	}
	return nil
}

// cypherExplainParameters gives every input a value of its type so the
// statement can be planned. An input's example is used when it has one: that is
// the value the author last ran the statement with, and a placeholder may not
// suit a typed property the way a real value does.
func cypherExplainParameters(inputs []*interfaces.ParameterDef) map[string]any {
	values := make(map[string]any, len(inputs))
	for _, input := range inputs {
		if input == nil {
			continue
		}
		if input.Example != nil {
			if converted, err := convertCypherArgument(input, input.Example); err == nil {
				values[input.Name] = converted
				continue
			}
		}
		values[input.Name] = cypherPlaceholder(input)
	}
	return values
}

func cypherPlaceholder(input *interfaces.ParameterDef) any {
	switch input.Type {
	case interfaces.ParameterTypeNumber:
		return 0
	case interfaces.ParameterTypeBoolean:
		return false
	case interfaces.ParameterTypeArray:
		item := &interfaces.ParameterDef{Type: interfaces.ParameterTypeString}
		if len(input.SubParameters) == 1 && input.SubParameters[0] != nil {
			item = input.SubParameters[0]
		}
		// IN refuses an empty list, so the placeholder holds one element.
		return []any{cypherPlaceholder(item)}
	default:
		return "x"
	}
}

// executeCypherFunction runs a Cypher function for one call. Its result has the
// shape every tool result has, so a refusal from bkn-backend, or arguments
// that do not fit, come back as a status and an error body rather than as a
// failure of the execution service itself: the tool ran, and this is what it
// said.
func (s *ToolServiceImpl) executeCypherFunction(ctx context.Context, req *interfaces.ExecuteToolReq,
	function *model.FunctionMetadataDB) (*interfaces.HTTPResponse, error) {
	start := time.Now()
	if s.BknBackend == nil {
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusServiceUnavailable, "cypher functions are unavailable: bkn-backend is not configured")
	}
	inputs, _ := parsers.FunctionParamsFromAPISpec(function.APISpec)
	parameters, argErr := cypherArguments(req.Body, inputs)
	if argErr != nil {
		return cypherErrorResponse(ctx, start, http.StatusBadRequest, argErr.Error()), nil
	}
	query := &interfaces.CypherQueryReq{Query: function.Code, Parameters: parameters}
	// The same final check every outbound tool call passes, immediately before it.
	if err := proxyexecution.AuthorizeOutbound(ctx, s.ProxyAuthorizer, s.ProxyAudit); err != nil {
		return nil, err
	}
	// The tool timeout is how long the caller waits. It is not sent as the
	// statement's timeout: bkn-backend refuses one above its own configured limit
	// (ontology-query always asks for 300 seconds), and that limit already ends
	// the statement sooner than any caller waits.
	callCtx := ctx
	if req.Timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}
	result, err := s.BknBackend.RunCypher(callCtx, function.KnID, query)
	if err != nil {
		if stderrors.Is(callCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return cypherErrorResponse(ctx, start, http.StatusGatewayTimeout,
				fmt.Sprintf("the cypher function did not finish within %d seconds", req.Timeout)), nil
		}
		return nil, err
	}
	if !isSuccess(result.StatusCode) {
		return &interfaces.HTTPResponse{
			StatusCode: result.StatusCode,
			Headers:    map[string]any{},
			Body:       decodeJSONBody(result.Body),
			Duration:   time.Since(start).Milliseconds(),
		}, nil
	}
	reply := cypherRunReply{}
	// Numbers stay as written: a 19-digit id must not come back rounded.
	decoder := json.NewDecoder(bytes.NewReader(result.Body))
	decoder.UseNumber()
	if err = decoder.Decode(&reply); err != nil {
		return nil, oerrors.DefaultHTTPError(ctx, http.StatusBadGateway, "bkn-backend returned an unreadable cypher result")
	}
	var body any
	if interfaces.CypherResultShape(function.ResultShape) == interfaces.CypherResultShapeValue {
		value, shapeErr := cypherSingleValue(&reply)
		if shapeErr != nil {
			return cypherErrorResponse(ctx, start, http.StatusBadRequest, shapeErr.Error()), nil
		}
		body = map[string]any{"value": value}
	} else {
		if reply.Columns == nil {
			reply.Columns = []json.RawMessage{}
		}
		if reply.Entries == nil {
			reply.Entries = []map[string]any{}
		}
		body = map[string]any{"columns": reply.Columns, "entries": reply.Entries, "limited": reply.Limited}
	}
	return &interfaces.HTTPResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string]any{},
		Body:       body,
		Duration:   time.Since(start).Milliseconds(),
	}, nil
}

// cypherSingleValue reads the one value a value function returns. More than
// one row is refused rather than cut to the first: which row came first can
// change from one run to the next, and a property must not.
func cypherSingleValue(reply *cypherRunReply) (any, error) {
	if len(reply.Columns) != 1 {
		return nil, fmt.Errorf("a value cypher function must return exactly one column, the statement returned %d", len(reply.Columns))
	}
	column := cypherColumn{}
	_ = json.Unmarshal(reply.Columns[0], &column)
	if kind, ok := reply.ValueTypes[column.Name]; ok && graphValueKinds[kind.Kind] {
		return nil, fmt.Errorf("a value cypher function must return a string, a number or a boolean, column %q is of kind %s", column.Name, kind.Kind)
	}
	switch len(reply.Entries) {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, fmt.Errorf("a value cypher function must return at most one row, the statement returned %d%s; "+
			"aggregate it, for example with count() or max()", len(reply.Entries), moreRowsHint(reply.Limited))
	}
	value := reply.Entries[0][column.Name]
	switch value.(type) {
	case nil, string, bool, float64, json.Number, int, int64:
		return value, nil
	default:
		return nil, fmt.Errorf("a value cypher function must return a string, a number or a boolean, column %q returned %s",
			column.Name, describeValue(value))
	}
}

func moreRowsHint(limited bool) string {
	if limited {
		return " or more"
	}
	return ""
}

// cypherArguments turns the call's arguments into the statement's parameters.
// Every input is required: bkn-backend has no optional parameter, a statement
// using $name fails without one. Values are converted to the declared type,
// so an agent that sends 5 as "5" still gets a number compared with a number.
// Arguments the function does not declare are left out rather than refused, the
// way any tool ignores fields its schema does not name.
func cypherArguments(body any, inputs []*interfaces.ParameterDef) (map[string]any, error) {
	args, err := argumentObject(body)
	if err != nil {
		return nil, err
	}
	parameters := make(map[string]any, len(inputs))
	var missing []string
	for _, input := range inputs {
		if input == nil {
			continue
		}
		value, ok := args[input.Name]
		if !ok || value == nil {
			missing = append(missing, input.Name)
			continue
		}
		converted, convErr := convertCypherArgument(input, value)
		if convErr != nil {
			return nil, convErr
		}
		parameters[input.Name] = converted
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required parameters: %s", strings.Join(missing, ", "))
	}
	return parameters, nil
}

func argumentObject(body any) (map[string]any, error) {
	switch typed := body.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return typed, nil
	case string:
		if strings.TrimSpace(typed) == "" {
			return map[string]any{}, nil
		}
		return decodeArgumentObject([]byte(typed))
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("arguments must be a JSON object")
		}
		return decodeArgumentObject(raw)
	}
}

// decodeArgumentObject keeps numbers as written, so a long id is not rounded.
func decodeArgumentObject(raw []byte) (map[string]any, error) {
	args := map[string]any{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&args); err != nil {
		return nil, fmt.Errorf("arguments must be a JSON object")
	}
	return args, nil
}

// jsonNumber reads a number written as a string. Only a JSON number is taken:
// ParseFloat would also accept Inf, NaN and hex, which cannot be sent on.
func jsonNumber(text string) (json.Number, bool) {
	var value any
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(text)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil || decoder.More() {
		return "", false
	}
	number, ok := value.(json.Number)
	return number, ok
}

func convertCypherArgument(input *interfaces.ParameterDef, value any) (any, error) {
	switch input.Type {
	case interfaces.ParameterTypeNumber:
		switch typed := value.(type) {
		case float64, int, int64, json.Number:
			return typed, nil
		case string:
			// Kept as written, so a long number is not rounded through a float.
			if number, ok := jsonNumber(typed); ok {
				return number, nil
			}
		}
	case interfaces.ParameterTypeBoolean:
		switch typed := value.(type) {
		case bool:
			return typed, nil
		case string:
			if parsed, err := strconv.ParseBool(strings.TrimSpace(typed)); err == nil {
				return parsed, nil
			}
		}
	case interfaces.ParameterTypeArray:
		list, ok := value.([]any)
		if !ok {
			break
		}
		item := &interfaces.ParameterDef{Name: input.Name, Type: interfaces.ParameterTypeString}
		if len(input.SubParameters) == 1 && input.SubParameters[0] != nil {
			item = &interfaces.ParameterDef{Name: input.Name, Type: input.SubParameters[0].Type}
		}
		converted := make([]any, 0, len(list))
		for _, element := range list {
			if element == nil {
				return nil, fmt.Errorf("parameter %s holds a null; a list parameter holds values only", input.Name)
			}
			value, err := convertCypherArgument(item, element)
			if err != nil {
				return nil, err
			}
			converted = append(converted, value)
		}
		return converted, nil
	default:
		switch typed := value.(type) {
		case string:
			return typed, nil
		case float64:
			// A code sent without quotes: the same digits, never 7.6904023e+07.
			return strconv.FormatFloat(typed, 'f', -1, 64), nil
		case int, int64, json.Number, bool:
			return fmt.Sprint(typed), nil
		}
	}
	return nil, fmt.Errorf("parameter %s must be %s, got %s", input.Name, describeType(input), describeValue(value))
}

func describeType(input *interfaces.ParameterDef) string {
	switch input.Type {
	case interfaces.ParameterTypeNumber:
		return "a number"
	case interfaces.ParameterTypeBoolean:
		return "a boolean"
	case interfaces.ParameterTypeArray:
		return "a list"
	default:
		return "a string"
	}
}

func describeValue(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case float64, int, int64, json.Number:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		return "an object"
	default:
		return "an unsupported value"
	}
}

func isSuccess(code int) bool { return code >= http.StatusOK && code < http.StatusMultipleChoices }

// cypherRefusal turns bkn-backend's refusal of a statement being saved into an
// error the author can act on: its own status, and its own words.
func cypherRefusal(ctx context.Context, result *interfaces.CypherQueryResult) error {
	status := result.StatusCode
	if status >= http.StatusInternalServerError || status < http.StatusBadRequest {
		status = http.StatusBadGateway
	}
	details := decodeJSONBody(result.Body)
	if refusal, ok := details.(map[string]any); ok {
		message := strings.TrimSpace(fmt.Sprint(refusal["description"]))
		if extra, ok := refusal["error_details"]; ok && extra != nil && fmt.Sprint(extra) != "" {
			message = strings.TrimSpace(message + ": " + fmt.Sprint(extra))
		}
		if message != "" && message != "<nil>" {
			return oerrors.DefaultHTTPError(ctx, status, "the cypher statement was refused: "+message)
		}
	}
	return oerrors.DefaultHTTPError(ctx, status, "the cypher statement was refused by bkn-backend")
}

func cypherErrorResponse(ctx context.Context, start time.Time, status int, message string) *interfaces.HTTPResponse {
	httpErr := oerrors.DefaultHTTPError(ctx, status, message)
	// Embedded in the tool's reply, it is not rendered on the way out as an error response is.
	_ = httpErr.Render()
	return &interfaces.HTTPResponse{
		StatusCode: status,
		Headers:    map[string]any{},
		Body:       httpErr.BaseError,
		Error:      message,
		Duration:   time.Since(start).Milliseconds(),
	}
}

func decodeJSONBody(body []byte) any {
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return string(body)
	}
	return decoded
}
