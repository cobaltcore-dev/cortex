// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// authProjectScope configures how the project ID is extracted from the
// request for project-scoped authorization.
type authProjectScope struct {
	// From selects the extraction strategy: "query" reads a URL query
	// parameter, "body" reads a top-level JSON body field.
	From string `json:"from"`
	// Param is the query parameter name (used when From is "query",
	// defaults to "project_id").
	Param string `json:"param,omitempty"`
	// Field is the JSON body field name (used when From is "body",
	// defaults to "project_id").
	Field string `json:"field,omitempty"`
}

// authPolicyRole is a role that grants access for a matching policy rule.
type authPolicyRole struct {
	// Name is the OpenStack role name (e.g. "cloud_compute_admin").
	Name string `json:"name"`
	// ProjectScope configures project-scoped authorization for this role.
	// When non-nil, the request's project ID (extracted per the config)
	// must match the token's project ID.
	ProjectScope *authProjectScope `json:"projectScope,omitempty"`
}

// authPolicy maps an HTTP method + path pattern to the roles allowed to
// access it. Patterns use "METHOD /path" syntax where "*" matches any
// method and "*" in the path acts as a wildcard. Evaluation is
// first-match; no match means deny.
type authPolicy struct {
	// Pattern is the method + path to match (e.g. "GET /usages", "* /*").
	Pattern string `json:"pattern"`
	// Roles lists the roles that grant access for this pattern.
	// When null, the path is publicly accessible (no token required).
	Roles []authPolicyRole `json:"roles"`
}

// authConfig configures the Keystone token-validation middleware.
// When nil, auth is disabled and all requests are passed through.
type authConfig struct {
	// TokenCacheTTL is how long validated tokens are cached before
	// re-introspection against Keystone (e.g. "5m").
	TokenCacheTTL string `json:"tokenCacheTTL,omitempty"`
	// Policies is the ordered list of first-match access rules evaluated
	// against each incoming request.
	Policies []authPolicy `json:"policies,omitempty"`
}

// compileAuthPolicies parses the auth config into the shim's runtime
// policy table and token cache. Called during SetupWithManager.
func (s *Shim) compileAuthPolicies() error {
	if s.config.Auth == nil {
		return nil
	}
	ttlStr := s.config.Auth.TokenCacheTTL
	if ttlStr == "" {
		ttlStr = "5m"
	}
	ttl, err := time.ParseDuration(ttlStr)
	if err != nil {
		return fmt.Errorf("invalid tokenCacheTTL %q: %w", ttlStr, err)
	}
	s.tokenCache = &tokenCache{ttl: ttl}
	s.authPolicies = make([]compiledPolicy, len(s.config.Auth.Policies))
	for i, p := range s.config.Auth.Policies {
		method, path, ok := strings.Cut(p.Pattern, " ")
		if !ok || method == "" || path == "" {
			return fmt.Errorf("invalid auth policy pattern %q: expected \"METHOD /path\"", p.Pattern)
		}
		roles, err := compileRoles(p.Roles)
		if err != nil {
			return fmt.Errorf("policy %q: %w", p.Pattern, err)
		}
		s.authPolicies[i] = compiledPolicy{
			method:      method,
			pathPattern: path,
			roles:       roles,
		}
	}
	setupLog.Info("Auth middleware configured",
		"policies", len(s.authPolicies), "tokenCacheTTL", ttl)
	return nil
}

func compileRoles(roles []authPolicyRole) ([]compiledRole, error) {
	if len(roles) == 0 {
		return nil, nil
	}
	compiled := make([]compiledRole, len(roles))
	for i, role := range roles {
		cr := compiledRole{name: role.Name}
		scope := role.ProjectScope
		if scope != nil {
			switch scope.From {
			case "query":
				cr.extractor = queryParamScope
				cr.extractParam = scope.Param
				if cr.extractParam == "" {
					cr.extractParam = "project_id"
				}
			case "body":
				cr.extractor = bodyFieldScope
				cr.extractParam = scope.Field
				if cr.extractParam == "" {
					cr.extractParam = "project_id"
				}
			default:
				return nil, fmt.Errorf("role %q: invalid projectScope.from %q (must be \"query\" or \"body\")", role.Name, scope.From)
			}
		}
		compiled[i] = cr
	}
	return compiled, nil
}

// extractBodyField reads the request body, extracts a top-level JSON
// string field, and re-wraps r.Body so downstream handlers can still
// read it.
func extractBodyField(r *http.Request, field string) string {
	if r.Body == nil || r.Body == http.NoBody {
		return ""
	}
	const maxBodySize = 1 << 20 // 1 MiB
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return ""
	}
	raw, ok := top[field]
	if !ok {
		return ""
	}
	var val string
	if err := json.Unmarshal(raw, &val); err != nil {
		return ""
	}
	return val
}

// tokenInfo holds validated Keystone token metadata cached after a
// successful introspection.
type tokenInfo struct {
	roles     []string
	projectID string
	expiresAt time.Time
	cachedAt  time.Time
}

// tokenCache is a concurrency-safe cache mapping opaque token strings
// to their validated tokenInfo. Entries are lazily evicted on lookup
// when either the Keystone token has expired or the cache TTL has
// elapsed.
type tokenCache struct {
	entries sync.Map // map[string]*tokenInfo
	sf      singleflight.Group
	ttl     time.Duration
}

func tokenCacheKey(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

func (c *tokenCache) get(token string) (*tokenInfo, bool) {
	key := tokenCacheKey(token)
	v, ok := c.entries.Load(key)
	if !ok {
		return nil, false
	}
	info := v.(*tokenInfo)
	now := time.Now()
	if now.After(info.expiresAt) || now.After(info.cachedAt.Add(c.ttl)) {
		c.entries.Delete(key)
		return nil, false
	}
	return info, true
}

func (c *tokenCache) put(token string, info *tokenInfo) {
	c.entries.Store(tokenCacheKey(token), info)
}

func (c *tokenCache) delete(token string) {
	c.entries.Delete(tokenCacheKey(token))
}

// tokenIntrospector abstracts Keystone token validation so tests can
// provide a mock without a real Keystone server.
type tokenIntrospector interface {
	introspect(ctx context.Context, tokenValue string) (*tokenInfo, error)
}

// projectIDExtractor selects how the request's project ID is obtained
// for project-scoped authorization checks.
type projectIDExtractor int

const (
	noProjectScope  projectIDExtractor = iota
	queryParamScope                    // extract from URL query parameter
	bodyFieldScope                     // extract from top-level JSON body field
)

// compiledRole is a pre-resolved role with its project-scope extraction
// strategy ready for request-time evaluation.
type compiledRole struct {
	name         string
	extractor    projectIDExtractor
	extractParam string // query param name or body field name
}

// compiledPolicy is an authPolicy with its pattern pre-parsed at setup
// time for efficient request-time matching.
type compiledPolicy struct {
	method      string         // HTTP method or "*"
	pathPattern string         // path with optional trailing "/*" wildcard
	roles       []compiledRole // nil/empty = public (no token required)
}

// matchPath reports whether requestPath matches the pattern.
// A trailing "/*" matches any suffix; "/*" alone matches everything.
func matchPath(pattern, requestPath string) bool {
	if pattern == "/*" || pattern == "*" {
		return true
	}
	if prefix, ok := strings.CutSuffix(pattern, "/*"); ok {
		return requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/")
	}
	return pattern == requestPath
}

func matchPolicy(p *compiledPolicy, method, path string) bool {
	if p.method != "*" && p.method != method {
		return false
	}
	return matchPath(p.pathPattern, path)
}

// authError writes an OpenStack-compatible JSON error response matching
// the format returned by the Placement API.
func authError(w http.ResponseWriter, code int, title, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	body := fmt.Sprintf(`{"error":{"code":%d,"title":%q,"message":%q}}`,
		code, title, message)
	//nolint:errcheck // best-effort write; nothing useful to do on failure
	w.Write([]byte(body))
}

// checkAuth validates the request's X-Auth-Token against the compiled
// policy table. It returns true if the request is authorized, false if
// a 401/403 response has already been written.
func (s *Shim) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if len(s.authPolicies) == 0 {
		return true
	}

	log := logf.FromContext(r.Context())

	var matched *compiledPolicy
	for i := range s.authPolicies {
		if matchPolicy(&s.authPolicies[i], r.Method, r.URL.Path) {
			matched = &s.authPolicies[i]
			break
		}
	}

	if matched == nil {
		log.Info("auth denied: no matching policy",
			"method", r.Method, "path", r.URL.Path)
		authError(w, http.StatusForbidden, "Forbidden",
			"No policy matches the requested resource.")
		return false
	}

	if len(matched.roles) == 0 {
		return true
	}

	tokenValue := r.Header.Get("X-Auth-Token")
	if tokenValue == "" {
		log.Info("auth denied: missing token",
			"method", r.Method, "path", r.URL.Path)
		authError(w, http.StatusUnauthorized, "Unauthorized",
			"The request you have made requires authentication.")
		return false
	}

	info, ok := s.tokenCache.get(tokenValue)
	if !ok {
		key := tokenCacheKey(tokenValue)
		v, err, _ := s.tokenCache.sf.Do(key, func() (any, error) {
			if cached, ok := s.tokenCache.get(tokenValue); ok {
				return cached, nil
			}
			result, err := s.tokenIntrospector.introspect(r.Context(), tokenValue)
			if err != nil {
				return nil, err
			}
			s.tokenCache.put(tokenValue, result)
			return result, nil
		})
		if err != nil {
			log.Info("token introspection failed", "error", err)
			authError(w, http.StatusUnauthorized, "Unauthorized",
				"Token validation failed.")
			return false
		}
		info = v.(*tokenInfo)
	}

	if time.Now().After(info.expiresAt) {
		s.tokenCache.delete(tokenValue)
		authError(w, http.StatusUnauthorized, "Unauthorized",
			"The token has expired.")
		return false
	}

	var bodyProjectID string
	var bodyRead bool

	for _, cr := range matched.roles {
		for _, tokenRole := range info.roles {
			if !strings.EqualFold(cr.name, tokenRole) {
				continue
			}
			switch cr.extractor {
			case noProjectScope:
				return true
			case queryParamScope:
				if id := r.URL.Query().Get(cr.extractParam); id != "" && id == info.projectID {
					return true
				}
			case bodyFieldScope:
				if !bodyRead {
					bodyProjectID = extractBodyField(r, cr.extractParam)
					bodyRead = true
				}
				if bodyProjectID != "" && bodyProjectID == info.projectID {
					return true
				}
			}
		}
	}

	log.Info("auth denied: insufficient roles",
		"method", r.Method, "path", r.URL.Path)
	authError(w, http.StatusForbidden, "Forbidden",
		"You do not have the required role for this operation.")
	return false
}
