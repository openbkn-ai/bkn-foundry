package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/interfaces"
)

const (
	defaultBufferSize = 4096
)

// StreamProcessor stream processor.
type StreamProcessor struct {
	logger interfaces.Logger
}

// NewStreamProcessor creates a stream processor.
func NewStreamProcessor(logger interfaces.Logger) *StreamProcessor {
	return &StreamProcessor{
		logger: logger,
	}
}

// ProcessSSE processes SSE streams.
func (p *StreamProcessor) ProcessSSE(ctx context.Context, reader io.Reader, writer io.Writer, isSSE bool) error {
	bufReader := bufio.NewReader(reader)
	var (
		// Record whether the last line is an end tag.
		receivedDone bool
		// Log whether any data was received.
		receivedData bool
		buffer       bytes.Buffer
	)

	if !isSSE {
		if _, err := buffer.ReadFrom(bufReader); err != nil {
			return fmt.Errorf("read non sse data failed: %w", err)
		}
		rawContent := buffer.String()
		// Return the entire response content as a single SSE data.
		// Ensure that data in JSON and other formats are not split and maintain integrity.
		trimmedContent := strings.TrimRight(rawContent, "\n")
		// Compress multiple lines of content into a single line, maintaining JSON integrity.
		singleLineContent := strings.ReplaceAll(trimmedContent, "\n", "")
		if _, err := fmt.Fprintf(writer, "data: %s\n\n", singleLineContent); err != nil {
			return err
		}
		receivedData = true
	} else {
		// Original streaming logic.
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				line, err := bufReader.ReadBytes('\n')
				if err != nil {
					if err == io.EOF {
						return p.checkReceivedData(writer, receivedData, receivedDone)
					}
					return fmt.Errorf("read sse data failed: %w", err)
				}

				if len(line) == 0 {
					continue
				}
				receivedData = true
				// Mark complete if [data: [DONE]] is included.
				if strings.Contains(string(line), "data: [DONE]") {
					lineStr := strings.TrimRight(string(line), "\n")
					if strings.TrimSpace(lineStr) == "data: [DONE]" {
						receivedDone = true
					}
				}
				if _, err = fmt.Fprintf(writer, "%s", line); err != nil {
					return err
				}
			}
		}
	}

	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return p.checkReceivedData(writer, receivedData, receivedDone)
}

// ProcessHTTPStream processes HTTP streams.
func (p *StreamProcessor) ProcessHTTPStream(ctx context.Context, reader io.Reader, writer io.Writer) error {
	buffer := make([]byte, defaultBufferSize) // 4KB buffer.
	// Log whether any data was received.
	var receivedData bool
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			n, err := reader.Read(buffer)
			if n > 0 {
				receivedData = true
				if _, writeErr := writer.Write(buffer[:n]); writeErr != nil {
					return writeErr
				}

				if flusher, ok := writer.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			if err == nil {
				continue
			}
			if err != io.EOF {
				return fmt.Errorf("read http stream data failed: %w", err)
			}
			return p.checkReceivedData(writer, receivedData, true)
		}
	}
}

// Check received data.
func (p *StreamProcessor) checkReceivedData(writer io.Writer, receivedData, receivedDone bool) (err error) {
	if !receivedData {
		// Send error message.
		err = errors.New("server does not support streaming or not data")
		return
	}
	// At the end of the stream, sent only if no [DONE] token has been received.
	if receivedDone {
		return
	}
	if _, err := fmt.Fprintf(writer, "%s\n\n", "data: [DONE]"); err != nil {
		return fmt.Errorf("write done message failed: %w", err)
	}
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return
}
