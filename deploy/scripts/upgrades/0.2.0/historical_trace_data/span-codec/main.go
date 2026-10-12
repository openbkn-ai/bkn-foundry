package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type Request struct {
	Document         json.RawMessage `json:"document"`
	SourceDeployment string          `json:"source_deployment"`
}

type Sidecar struct {
	SourcePosition   string          `json:"source_position"`
	SourceDocument   json.RawMessage `json:"source_document"`
	UnknownFields    map[string]any  `json:"unknown_fields"`
	SourceDeployment string          `json:"source_deployment"`
	SourceHash       string          `json:"source_sha256"`
	NativeHash       string          `json:"native_sha256"`
	Codec            string          `json:"codec"`
}

type Span struct {
	Payload map[string]any `json:"payload"`
	Sidecar Sidecar        `json:"sidecar"`
}

type Response struct {
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason"`
	Spans    []Span `json:"spans"`
}

func run(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var req Request
		_, rawError := strictJSON(scanner.Bytes())
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || rawError != nil {
			if err := encoder.Encode(Response{Reason: "invalid_request_json", Spans: []Span{}}); err != nil {
				return err
			}
			continue
		}
		if err := encoder.Encode(convert(req)); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "span codec input/output failed")
		os.Exit(1)
	}
}
