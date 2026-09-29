package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/openbkn-ai/bkn-foundry/adp/execution-factory/operator-integration/server/infra/errors"
)

// PyPI source parser.

const (
	DefaultPypiRepo = "https://pypi.org/simple" // Default PyPI source.
)

// DefaultTrustedPypiIndexes is the trusted-mirror allowlist used when the deployment configures none.
//
// Trusted-mirror policy: metadata requests only ever go to an index listed in configuration
// (pypi.trusted_index_urls). A caller may pick one of those indexes through pypi_repo_url, but the
// request URL is always built from the configured entry, never from the caller's string, so a
// caller cannot point the service at an arbitrary (for example internal) host.
var DefaultTrustedPypiIndexes = []string{DefaultPypiRepo}

// Legal Python package name (PEP 508): starts and ends with alphanumeric characters, and can contain -_. separator in the middle.
// The package name will be spelled into the upstream URL. If there is no verification, input such as spaces will be escaped and sent as it is.
// Neither the result can be obtained nor the illegal input is sent to the external address.
var pypiPackageNamePattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// ParsePyPIReq parses PyPI source request parameters.
type ParsePypiReq struct {
	// PypiRepoURL selects one of the trusted indexes; empty selects the first trusted index.
	PypiRepoURL   string `form:"pypi_repo_url" validate:"omitempty,url"`
	PackageName   string `uri:"package_name" validate:"required"`
	PythonVersion string `form:"python_version" default:"3.10"`
}

// ParsePyPIResp parses PyPI source response parameters.
type ParsePypiResp struct {
	PackageName string   `json:"package_name"`
	Versions    []string `json:"versions"`
}

// PyPIResponse PyPI source response parameters.
type PypiResponse struct {
	Info struct {
		Name           string `json:"name"`
		Version        string `json:"version"`
		RequiresPython string `json:"requires_python"`
	} `json:"info"`
	Releases map[string][]PypiRelease `json:"releases"`
}

// PyPIRelease PyPI source response parameters.
type PypiRelease struct {
	RequiresPython string `json:"requires_python"`
	Yanked         bool   `json:"yanked"`
	YankedReason   string `json:"yanked_reason"`
}

// ParsePypi lists the versions of a package that support the requested Python version.
// trustedIndexes is the configured mirror allowlist; empty falls back to DefaultTrustedPypiIndexes.
func ParsePypi(ctx context.Context, req *ParsePypiReq, trustedIndexes []string) (resp *ParsePypiResp, err error) {
	packageName := strings.TrimSpace(req.PackageName)
	pythonVersion := strings.TrimSpace(req.PythonVersion)
	if packageName == "" {
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "package_name is empty")
	}
	if !pypiPackageNamePattern.MatchString(packageName) {
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest,
			fmt.Sprintf("invalid package_name %q: only letters, digits and - _ . are allowed", packageName))
	}
	if pythonVersion == "" {
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest, "python_version is empty")
	}

	targetPy, err := parsePythonVersion(pythonVersion)
	if err != nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest, fmt.Sprintf("invalid python_version: %s", err.Error()))
	}

	baseURL, err := resolveTrustedPypiIndex(req.PypiRepoURL, trustedIndexes)
	if err != nil {
		return nil, errors.DefaultHTTPError(ctx, http.StatusBadRequest, err.Error())
	}
	// JoinPath escapes each segment; the package name has already been checked against PEP 508.
	url := baseURL.JoinPath("pypi", packageName, "json").String()

	httpClient := pypiHTTPClient
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, errors.NewHTTPError(ctx, http.StatusInternalServerError, errors.ErrExtPypiParserFailed, fmt.Sprintf("create request failed: %s", err.Error()))
	}
	httpReq.Header.Set("User-Agent", "pypi-parser/1.0 (+https://pypi.org)")

	rsp, err := httpClient.Do(httpReq)
	if err != nil {
		// The failure to connect is a network problem, not a lack of JSON API in the mirror source. Use error codes to distinguish,
		// Otherwise, "Please check the mirror source configuration" will lead the troubleshooting direction to the configuration.
		return nil, errors.NewHTTPError(ctx, http.StatusInternalServerError, errors.ErrExtPypiRepoUnavailable,
			map[string]interface{}{"error": err.Error(), "url": url})
	}
	defer func() { _ = rsp.Body.Close() }()
	if rsp.StatusCode == http.StatusNotFound {
		return &ParsePypiResp{
			PackageName: packageName,
			Versions:    []string{},
		}, nil
	}

	if rsp.StatusCode != http.StatusOK {
		return nil, errors.NewHTTPError(ctx, http.StatusInternalServerError, errors.ErrExtPypiParserFailed, fmt.Sprintf("HTTP %s", rsp.Status))
	}

	body, err := io.ReadAll(rsp.Body)
	if err != nil {
		return nil, errors.NewHTTPError(ctx, http.StatusInternalServerError, errors.ErrExtPypiParserFailed, fmt.Sprintf("read ParsePypiResp failed: %s", err.Error()))
	}

	var pypiData PypiResponse
	if err := json.Unmarshal(body, &pypiData); err != nil {
		return nil, errors.NewHTTPError(ctx, http.StatusInternalServerError, errors.ErrExtPypiParserFailed, map[string]interface{}{
			"error": fmt.Sprintf("decode JSON failed: %s", err.Error()),
			"body":  string(body),
		})
	}
	validVersions := collectCompatibleVersions(pypiData, targetPy)
	sort.Sort(sort.Reverse(semver.Collection(validVersions)))

	versions := make([]string, 0, len(validVersions))
	for _, v := range validVersions {
		versions = append(versions, v.Original())
	}

	name := strings.TrimSpace(pypiData.Info.Name)
	if name == "" {
		name = packageName
	}

	return &ParsePypiResp{
		PackageName: name,
		Versions:    versions,
	}, nil
}

// pypiHTTPClient is shared across lookups so connections to the index are reused.
var pypiHTTPClient = &http.Client{Timeout: 25 * time.Second}

// parsePypiIndexURL parses an index URL into its JSON API root: trailing slashes and the "/simple"
// suffix are dropped, so "https://pypi.org/simple/" and "https://pypi.org" name the same index.
func parsePypiIndexURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid pypi_repo_url: %w", err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("invalid pypi_repo_url: scheme must be http or https")
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid pypi_repo_url: expected scheme://host[/path] without credentials, query or fragment")
	}
	path := strings.TrimRight(u.Path, "/")
	path = strings.TrimSuffix(path, "/simple")
	return &url.URL{Scheme: u.Scheme, Host: strings.ToLower(u.Host), Path: path}, nil
}

// resolveTrustedPypiIndex maps the caller's pypi_repo_url onto a configured trusted index and
// returns the configured entry, so the request host always comes from configuration. An empty
// repoURL selects the first trusted index.
func resolveTrustedPypiIndex(repoURL string, trustedIndexes []string) (*url.URL, error) {
	if len(trustedIndexes) == 0 {
		trustedIndexes = DefaultTrustedPypiIndexes
	}
	trusted := make([]*url.URL, 0, len(trustedIndexes))
	for _, raw := range trustedIndexes {
		u, err := parsePypiIndexURL(raw)
		if err != nil {
			// A malformed allowlist entry can never match; skipping it keeps the others usable.
			continue
		}
		trusted = append(trusted, u)
	}
	if len(trusted) == 0 {
		return nil, fmt.Errorf("no valid trusted PyPI index is configured")
	}
	if strings.TrimSpace(repoURL) == "" {
		return trusted[0], nil
	}
	want, err := parsePypiIndexURL(repoURL)
	if err != nil {
		return nil, err
	}
	for _, u := range trusted {
		if u.Scheme == want.Scheme && u.Host == want.Host && u.Path == want.Path {
			return u, nil
		}
	}
	return nil, fmt.Errorf("pypi_repo_url %q is not a trusted PyPI index; an administrator can add it to pypi.trusted_index_urls", repoURL)
}

func collectCompatibleVersions(pypiData PypiResponse, targetPy pythonVer) []*semver.Version {
	validVersions := make([]*semver.Version, 0, len(pypiData.Releases))
	for verStr, releases := range pypiData.Releases {
		pkgVer, err := semver.NewVersion(verStr)
		if err != nil {
			continue
		}
		if len(releases) == 0 {
			continue
		}

		ok := false
		for _, rel := range releases {
			if rel.Yanked {
				continue
			}
			spec := strings.TrimSpace(rel.RequiresPython)
			if spec == "" {
				spec = strings.TrimSpace(pypiData.Info.RequiresPython)
			}
			if pythonSpecSatisfied(spec, targetPy) {
				ok = true
				break
			}
		}
		if ok {
			validVersions = append(validVersions, pkgVer)
		}
	}
	return validVersions
}

type pythonVer struct {
	major     int
	minor     int
	patch     int
	specified int
}

func parsePythonVersion(s string) (pythonVer, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return pythonVer{}, fmt.Errorf("empty python version")
	}
	parts := strings.Split(s, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return pythonVer{}, fmt.Errorf("expect 'X', 'X.Y' or 'X.Y.Z', got '%s'", s)
	}
	maj, err := parseInt(parts[0])
	if err != nil {
		return pythonVer{}, err
	}
	min := 0
	patch := 0
	if len(parts) >= 2 {
		min, err = parseInt(parts[1])
		if err != nil {
			return pythonVer{}, err
		}
	}
	if len(parts) == 3 {
		patch, err = parseInt(parts[2])
		if err != nil {
			return pythonVer{}, err
		}
	}
	return pythonVer{major: maj, minor: min, patch: patch, specified: len(parts)}, nil
}

func parseInt(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty number")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid number: %s", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func pythonSpecSatisfied(spec string, target pythonVer) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return true
	}
	if i := strings.Index(spec, ";"); i >= 0 {
		spec = strings.TrimSpace(spec[:i])
		if spec == "" {
			return true
		}
	}

	clauses := strings.Split(spec, ",")
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		if !pythonClauseSatisfied(clause, target) {
			return false
		}
	}
	return true
}

func pythonClauseSatisfied(clause string, target pythonVer) bool {
	op, verStr := splitOperatorAndVersion(clause)
	if op == "" || verStr == "" {
		return false
	}

	if op == "~=" {
		base, wildcard, err := parseSpecVersion(verStr)
		if err != nil || wildcard {
			return false
		}
		upper := compatibleUpperBound(base)
		return comparePython(target, base) >= 0 && comparePython(target, upper) < 0
	}

	specVer, wildcard, err := parseSpecVersion(verStr)
	if err != nil {
		return false
	}

	switch op {
	case "==":
		if wildcard {
			return matchPrefix(target, specVer)
		}
		return comparePython(target, specVer) == 0
	case "!=":
		if wildcard {
			return !matchPrefix(target, specVer)
		}
		return comparePython(target, specVer) != 0
	case ">":
		return comparePython(target, specVer) > 0
	case ">=":
		return comparePython(target, specVer) >= 0
	case "<":
		return comparePython(target, specVer) < 0
	case "<=":
		return comparePython(target, specVer) <= 0
	default:
		return false
	}
}

func splitOperatorAndVersion(clause string) (string, string) {
	clause = strings.TrimSpace(clause)
	ops := []string{">=", "<=", "==", "!=", "~=", ">", "<"}
	for _, op := range ops {
		if strings.HasPrefix(clause, op) {
			return op, strings.TrimSpace(clause[len(op):])
		}
	}
	return "", ""
}

func parseSpecVersion(s string) (pythonVer, bool, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, ".*") {
		base := strings.TrimSuffix(s, ".*")
		v, err := parsePythonVersion(base)
		return v, true, err
	}
	v, err := parsePythonVersion(s)
	return v, false, err
}

func compatibleUpperBound(base pythonVer) pythonVer {
	if base.patch != 0 {
		return pythonVer{major: base.major, minor: base.minor + 1, patch: 0}
	}
	return pythonVer{major: base.major + 1, minor: 0, patch: 0}
}

func matchPrefix(target pythonVer, prefix pythonVer) bool {
	if prefix.specified <= 1 {
		return target.major == prefix.major
	}
	if prefix.specified == 2 {
		return target.major == prefix.major && target.minor == prefix.minor
	}
	return target.major == prefix.major && target.minor == prefix.minor && target.patch == prefix.patch
}

func comparePython(a, b pythonVer) int {
	if a.major != b.major {
		if a.major < b.major {
			return -1
		}
		return 1
	}
	if a.minor != b.minor {
		if a.minor < b.minor {
			return -1
		}
		return 1
	}
	if a.patch != b.patch {
		if a.patch < b.patch {
			return -1
		}
		return 1
	}
	return 0
}
