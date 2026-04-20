// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

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
	ttl     time.Duration
}

func (c *tokenCache) get(token string) (*tokenInfo, bool) {
	v, ok := c.entries.Load(token)
	if !ok {
		return nil, false
	}
	info := v.(*tokenInfo)
	now := time.Now()
	if now.After(info.expiresAt) || now.After(info.cachedAt.Add(c.ttl)) {
		c.entries.Delete(token)
		return nil, false
	}
	return info, true
}

func (c *tokenCache) put(token string, info *tokenInfo) {
	c.entries.Store(token, info)
}

// tokenIntrospector abstracts Keystone token validation so tests can
// provide a mock without a real Keystone server.
type tokenIntrospector interface {
	introspect(ctx context.Context, tokenValue string) (*tokenInfo, error)
}

// compiledPolicy is an authPolicy with its pattern pre-parsed at setup
// time for efficient request-time matching.
type compiledPolicy struct {
	method      string           // HTTP method or "*"
	pathPattern string           // path with optional trailing "/*" wildcard
	roles       []authPolicyRole // nil/empty = public (no token required)
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
	if s.authPolicies == nil {
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
		authError(w, http.StatusUnauthorized, "Unauthorized",
			"The request you have made requires authentication.")
		return false
	}

	info, ok := s.tokenCache.get(tokenValue)
	if !ok {
		var err error
		info, err = s.tokenIntrospector.introspect(r.Context(), tokenValue)
		if err != nil {
			log.Info("token introspection failed", "error", err)
			authError(w, http.StatusUnauthorized, "Unauthorized",
				"Token validation failed.")
			return false
		}
		s.tokenCache.put(tokenValue, info)
	}

	if time.Now().After(info.expiresAt) {
		s.tokenCache.entries.Delete(tokenValue)
		authError(w, http.StatusUnauthorized, "Unauthorized",
			"The token has expired.")
		return false
	}

	for _, policyRole := range matched.roles {
		for _, tokenRole := range info.roles {
			if policyRole.Name != tokenRole {
				continue
			}
			if policyRole.ProjectScoped {
				reqProjectID := r.URL.Query().Get("project_id")
				if reqProjectID == "" || reqProjectID != info.projectID {
					continue
				}
			}
			return true
		}
	}

	log.Info("auth denied: insufficient roles",
		"method", r.Method, "path", r.URL.Path)
	authError(w, http.StatusForbidden, "Forbidden",
		"You do not have the required role for this operation.")
	return false
}
