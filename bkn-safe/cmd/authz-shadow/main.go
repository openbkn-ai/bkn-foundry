// Copyright openbkn.ai
//
// Licensed under the OpenBKN License. See LICENSE-OPENBKN.txt in the project root.

// Command authz-shadow compares ISF authorization decisions with bkn-safe's for
// the same requests — the safety net for the per-service authz cutover.
//
// For each request {accessor, type, id, operation} it calls:
//   - ISF:      POST {isf}/api/authorization/v1/operation-check  -> {result}
//   - bkn-safe: POST {safe}/api/safe/v1/authz/check              -> {allowed}
// and reports MATCH / DIFF per request plus a summary. Use it to (a) batch-
// validate before flipping a service, and (b) understand the intentional deltas
// from the authz redesign.
//
// Usage:
//   authz-shadow -isf https://host -safe http://127.0.0.1:3000 -token <bearer> \
//                -corpus requests.json
// corpus.json: [{"accessor":"<id>","type":"agent","id":"probe","operation":"use"}, ...]
// If -corpus is omitted, requests are read from stdin (same JSON array).
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

type request struct {
	Accessor  string `json:"accessor"`
	Type      string `json:"type"`
	ID        string `json:"id"`
	Operation string `json:"operation"`
}

func main() {
	isf := flag.String("isf", "", "ISF base URL (e.g. https://10.211.55.4); empty = skip ISF, bkn-safe only")
	safe := flag.String("safe", "http://127.0.0.1:3000", "bkn-safe base URL")
	token := flag.String("token", "", "Bearer token for ISF auth (and bkn-safe if it enforces)")
	bizDomain := flag.String("biz-domain", "bd_public", "x-business-domain header for ISF")
	corpus := flag.String("corpus", "", "path to JSON array of requests; stdin if empty")
	insecure := flag.Bool("insecure", true, "skip TLS verify (self-signed dev certs)")
	flag.Parse()

	reqs, err := loadCorpus(*corpus)
	if err != nil {
		fail("load corpus: %v", err)
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: *insecure}},
	}

	var match, diff, errs int
	fmt.Printf("%-38s %-18s %-10s %-8s %-8s %s\n", "accessor", "resource", "op", "ISF", "bkn-safe", "verdict")
	for _, r := range reqs {
		var isfRes, safeRes string
		if *isf != "" {
			isfRes = boolStr(callISF(client, *isf, *token, *bizDomain, r))
		} else {
			isfRes = "-"
		}
		safeRes = boolStr(callSafe(client, *safe, *token, r))

		verdict := "MATCH"
		switch {
		case isfRes == "ERR" || safeRes == "ERR":
			verdict = "ERR"
			errs++
		case isfRes == "-":
			verdict = "SAFE-ONLY"
		case isfRes != safeRes:
			verdict = "DIFF"
			diff++
		default:
			match++
		}
		fmt.Printf("%-38s %-18s %-10s %-8s %-8s %s\n",
			trunc(r.Accessor, 38), trunc(r.Type+":"+r.ID, 18), trunc(r.Operation, 10), isfRes, safeRes, verdict)
	}
	fmt.Printf("\n== %d match, %d diff, %d err (of %d) ==\n", match, diff, errs, len(reqs))
	if diff > 0 || errs > 0 {
		os.Exit(1)
	}
}

// callISF posts to ISF operation-check; returns (allowed, ok).
func callISF(c *http.Client, base, token, biz string, r request) (bool, bool) {
	body := map[string]any{
		"accessor":  map[string]string{"type": "user", "id": r.Accessor},
		"resource":  map[string]string{"type": r.Type, "id": r.ID},
		"operation": []string{r.Operation},
		"method":    "GET",
	}
	hdr := map[string]string{"x-business-domain": biz}
	if token != "" {
		hdr["Authorization"] = "Bearer " + token
		hdr["token"] = token
	}
	var out struct {
		Result bool `json:"result"`
	}
	if !postJSON(c, base+"/api/authorization/v1/operation-check", body, hdr, &out) {
		return false, false
	}
	return out.Result, true
}

// callSafe posts to bkn-safe check; returns (allowed, ok).
func callSafe(c *http.Client, base, token string, r request) (bool, bool) {
	body := map[string]any{
		"accessor_id": r.Accessor,
		"resource":    map[string]string{"type": r.Type, "id": r.ID},
		"operation":   r.Operation,
	}
	hdr := map[string]string{}
	if token != "" {
		hdr["Authorization"] = "Bearer " + token
	}
	var out struct {
		Allowed bool `json:"allowed"`
	}
	if !postJSON(c, base+"/api/safe/v1/authz/check", body, hdr, &out) {
		return false, false
	}
	return out.Allowed, true
}

func postJSON(c *http.Client, url string, body any, hdr map[string]string, out any) bool {
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	return json.Unmarshal(data, out) == nil
}

func loadCorpus(path string) ([]request, error) {
	var r io.Reader = os.Stdin
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var reqs []request
	if err := json.Unmarshal(data, &reqs); err != nil {
		return nil, err
	}
	return reqs, nil
}

func boolStr(v bool, ok bool) string {
	if !ok {
		return "ERR"
	}
	if v {
		return "allow"
	}
	return "deny"
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

func fail(f string, a ...any) {
	fmt.Fprintf(os.Stderr, f+"\n", a...)
	os.Exit(2)
}
