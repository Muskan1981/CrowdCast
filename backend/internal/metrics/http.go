package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// UnmatchedRoute is the route label for requests that match no chi pattern
// (404s, scanners, typos). Using it instead of the raw path keeps label
// cardinality bounded.
const UnmatchedRoute = "unmatched"

// RoutePattern returns the chi route pattern that handled r, such as
// "/api/v1/events/{id}/chat", or UnmatchedRoute when nothing matched.
// It must be called after the handler chain has run, because chi fills the
// pattern in during routing.
func RoutePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return UnmatchedRoute
	}
	if p := rctx.RoutePattern(); p != "" {
		return p
	}
	return UnmatchedRoute
}

// labelRoute refines RoutePattern using the response status. A mounted
// subrouter such as /api/v1 reports its catch-all "/*" even for requests it
// does not handle, so a 404 under a catch-all is labelled "unmatched" instead.
func labelRoute(pattern string, status int) string {
	if status == http.StatusNotFound && strings.HasSuffix(pattern, "/*") {
		return UnmatchedRoute
	}
	return pattern
}

// HTTPMiddleware records request count and latency. Register it on the top-level
// router (r.Use) so chi has already attached its route context to the request.
func (m *Metrics) HTTPMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			m.requests.Add(1)

			next.ServeHTTP(rec, r)

			route := labelRoute(RoutePattern(r), rec.status)
			m.HTTPRequests.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()

			// A WebSocket lives for minutes; timing it would bury real latency in the histogram.
			if !isWebSocketUpgrade(r) {
				m.HTTPDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
			}
		})
	}
}

func isWebSocketUpgrade(r *http.Request) bool {
	return r.Header.Get("Upgrade") == "websocket"
}

// statusRecorder captures the status code. It forwards Hijack, Flush and Unwrap
// so WebSocket upgrades and http.ResponseController keep working.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("metrics: underlying ResponseWriter is not a Hijacker")
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
