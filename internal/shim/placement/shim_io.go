// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package placement

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"time"

	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// shimReader wraps an io.Reader and counts the bytes read through it.
type shimReader struct {
	r io.Reader
	n int64
}

func (sr *shimReader) Read(p []byte) (int, error) {
	n, err := sr.r.Read(p)
	sr.n += int64(n)
	return n, err
}

// shimWriter writes to an underlying writer up to a byte limit, silently
// discarding anything beyond that limit.
type shimWriter struct {
	w io.Writer
	n int64 // remaining bytes allowed
}

func (sw *shimWriter) Write(p []byte) (int, error) {
	if sw.n <= 0 {
		return len(p), nil // discard
	}
	if int64(len(p)) > sw.n {
		p = p[:sw.n]
	}
	n, err := sw.w.Write(p)
	sw.n -= int64(n)
	return n, err
}

// shimResponseWriter wraps http.ResponseWriter to capture the HTTP status
// code and count the bytes written to the response body. When bodyBuf is
// non-nil (debug level), it also captures up to bodyLimit bytes of the
// response body for logging.
type shimResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
	bodyBuf      *bytes.Buffer // non-nil only at debug level
	bodyLimit    int64         // max bytes to capture in bodyBuf
}

func (w *shimResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *shimResponseWriter) Write(b []byte) (int, error) {
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytesWritten += int64(n)
	if w.bodyBuf != nil && int64(w.bodyBuf.Len()) < w.bodyLimit {
		// Capture up to bodyLimit bytes for debug logging.
		remaining := w.bodyLimit - int64(w.bodyBuf.Len())
		if int64(n) <= remaining {
			w.bodyBuf.Write(b[:n])
		} else {
			w.bodyBuf.Write(b[:remaining])
		}
	}
	return n, err
}

// writeJSON serializes v as JSON and writes it to w with the given HTTP status
// code. On encoding failure it sends a 500 Internal Server Error instead.
func (s *Shim) writeJSON(w http.ResponseWriter, statusCode int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		logf.Log.Error(err, "failed to encode JSON response")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if _, err := w.Write(buf.Bytes()); err != nil {
		logf.Log.Error(err, "failed to write JSON response")
	}
}

// wrapHandler returns an http.HandlerFunc that wraps next with logging,
// metrics collection, and request-ID propagation. It is used by
// RegisterRoutes to apply uniform middleware to every placement API handler.
func (s *Shim) wrapHandler(pattern string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		ctx = context.WithValue(ctx, routePatternKey, pattern)

		// Extract X-OpenStack-Request-Id for tracing and add it to the
		// context and logger so all downstream code includes it.
		reqID := r.Header.Get("X-OpenStack-Request-Id")
		log := logf.FromContext(ctx)
		if reqID != "" {
			log = log.WithValues("requestID", reqID)
			ctx = context.WithValue(ctx, requestIDKey, reqID)
		}
		ctx = logf.IntoContext(ctx, log)
		r = r.WithContext(ctx)

		debug := log.V(1).Enabled()

		// Wrap the request body to count bytes read. At debug level,
		// also tee the body into a limited buffer for logging.
		sr := &shimReader{r: r.Body}
		var reqBodyBuf *bytes.Buffer
		if debug && r.Body != nil && r.Body != http.NoBody {
			reqBodyBuf = &bytes.Buffer{}
			sr.r = io.TeeReader(r.Body, &shimWriter{w: reqBodyBuf, n: s.maxBodyLogSize})
		}
		r.Body = io.NopCloser(sr)

		// Wrap the response writer to capture status code, body size,
		// and optionally body content at debug level.
		sw := &shimResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
			bodyLimit:      s.maxBodyLogSize,
		}
		if debug {
			sw.bodyBuf = &bytes.Buffer{}
		}

		start := time.Now()
		if s.checkAuth(sw, r) {
			next.ServeHTTP(sw, r)
		}
		latencyMs := time.Since(start).Milliseconds()

		// NOTE: We intentionally never log HTTP headers to avoid
		// leaking X-Auth-Token or other sensitive header values.
		log.Info("handled request",
			"method", r.Method,
			"path", r.URL.Path,
			"query", r.URL.RawQuery,
			"status", sw.statusCode,
			"latencyMs", latencyMs,
			"requestSize", sr.n,
			"responseSize", sw.bytesWritten,
		)

		if debug {
			if reqBodyBuf != nil && reqBodyBuf.Len() > 0 {
				log.V(1).Info("request body", "body", reqBodyBuf.String())
			} else {
				log.V(1).Info("request body", "body", "<empty>")
			}
			if sw.bodyBuf != nil && sw.bodyBuf.Len() > 0 {
				log.V(1).Info("response body", "body", sw.bodyBuf.String())
			} else {
				log.V(1).Info("response body", "body", "<empty>")
			}
		}

		s.downstreamRequestTimer.
			WithLabelValues(r.Method, pattern, strconv.Itoa(sw.statusCode)).
			Observe(time.Since(start).Seconds())
	}
}
