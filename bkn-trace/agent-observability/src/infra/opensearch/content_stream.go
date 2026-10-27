// Copyright (c) 2026 OpenBKN
// SPDX-License-Identifier: LicenseRef-OpenBKN
// Licensed under the OpenBKN License, a modified Apache 2.0 with Additional
// Conditions. See LICENSE-OPENBKN.txt in the repository root for the full text.

package opensearch

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// OpenContentJSON streams only a stored content_json string from an exact
// document version. Filtering is server-side; redirects and oversized responses
// are refused. The caller must close even if hashing or validation fails.
type ContentJSONReader interface {
	io.ReadCloser
	ReadBytes() int64
}

func (c *Client) OpenContentJSON(ctx context.Context, index, id string, version, maxBytes int64) (ContentJSONReader, error) {
	if version <= 0 || maxBytes <= 0 || maxBytes > 32<<20 {
		return nil, ErrInvalidReadBudget
	}
	requestURL := fmt.Sprintf("%s/%s/_doc/%s?%s", c.baseURL, strings.TrimLeft(index, "/"), url.PathEscape(id), url.Values{"filter_path": []string{"_source.content_json"}, "version": []string{strconv.FormatInt(version, 10)}}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	if c.auth.Enabled {
		req.SetBasicAuth(c.auth.Username, c.auth.Password)
	}
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, &StatusError{Operation: "content stream", StatusCode: resp.StatusCode}
	}
	limited := &contentResponseReader{reader: resp.Body, remaining: maxBytes}
	stream := &contentJSONStream{body: resp.Body, raw: limited, reader: bufio.NewReaderSize(limited, 32<<10)}
	for _, expected := range []byte(`{"_source":{"content_json":"`) {
		b, e := stream.reader.ReadByte()
		if e != nil || b != expected {
			_ = resp.Body.Close()
			return nil, errors.New("invalid content stream envelope")
		}
	}
	return stream, nil
}

type contentResponseReader struct {
	reader    io.Reader
	bytes     int64
	remaining int64
}

func (r *contentResponseReader) Read(p []byte) (int, error) {
	if r.remaining < 0 {
		return 0, ErrResponseReadBudget
	}
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	r.bytes += int64(n)
	if r.remaining < 0 {
		return 0, ErrResponseReadBudget
	}
	return n, err
}

type contentJSONStream struct {
	raw     *contentResponseReader
	body    io.Closer
	reader  *bufio.Reader
	pending []byte
	done    bool
}

func (s *contentJSONStream) ReadBytes() int64 { return s.raw.bytes }

func (s *contentJSONStream) Close() error { return s.body.Close() }
func (s *contentJSONStream) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if len(s.pending) > 0 {
			copied := copy(p[n:], s.pending)
			n += copied
			s.pending = s.pending[copied:]
			continue
		}
		if s.done {
			if n > 0 {
				return n, nil
			}
			return 0, io.EOF
		}
		b, err := s.reader.ReadByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return n, err
		}
		if b == '"' {
			for _, want := range []byte("}}") {
				end, e := s.reader.ReadByte()
				if e != nil || end != want {
					return n, errors.New("invalid content stream end")
				}
			}
			for {
				end, e := s.reader.ReadByte()
				if e == io.EOF {
					break
				}
				if e != nil {
					return n, e
				}
				if end != ' ' && end != '\n' && end != '\r' && end != '\t' {
					return n, errors.New("content stream trailing data")
				}
			}
			s.done = true
			continue
		}
		if b < 0x20 {
			return n, errors.New("invalid content string control character")
		}
		if b != '\\' {
			p[n] = b
			n++
			continue
		}
		escape, e := s.reader.ReadByte()
		if e != nil {
			return n, e
		}
		switch escape {
		case '"', '\\', '/':
			s.pending = []byte{escape}
		case 'b':
			s.pending = []byte{'\b'}
		case 'f':
			s.pending = []byte{'\f'}
		case 'n':
			s.pending = []byte{'\n'}
		case 'r':
			s.pending = []byte{'\r'}
		case 't':
			s.pending = []byte{'\t'}
		case 'u':
			r, e := s.readHexRune()
			if e != nil {
				return n, e
			}
			if utf16.IsSurrogate(r) {
				slash, e := s.reader.ReadByte()
				if e != nil {
					return n, e
				}
				u, e := s.reader.ReadByte()
				if e != nil || slash != '\\' || u != 'u' {
					return n, errors.New("invalid content surrogate pair")
				}
				low, e := s.readHexRune()
				if e != nil {
					return n, e
				}
				r = utf16.DecodeRune(r, low)
				if r == utf8.RuneError {
					return n, errors.New("invalid content surrogate pair")
				}
			}
			s.pending = []byte(string(r))
		default:
			return n, errors.New("invalid content string escape")
		}
	}
	return n, nil
}
func (s *contentJSONStream) readHexRune() (rune, error) {
	var raw [4]byte
	if _, err := io.ReadFull(s.reader, raw[:]); err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(string(raw[:]), 16, 16)
	return rune(v), err
}
