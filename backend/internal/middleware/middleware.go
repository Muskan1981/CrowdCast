package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/crowdcast/crowdcast/internal/metrics"
	"github.com/go-chi/cors"
	"github.com/google/uuid"
)

type contextKey string

const (
	RequestIDKey    contextKey = "request_id"
	HeaderRequestID string     = "X-Request-ID"
)

// GetRequestID returns the request ID from the context, or an empty string.
func GetRequestID(ctx context.Context) string {
	if val, ok := ctx.Value(RequestIDKey).(string); ok {
		return val
	}
	return ""
}

// RequestID ensures every request has a unique request ID.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := strings.TrimSpace(r.Header.Get(HeaderRequestID))
		if reqID == "" {
			reqID = uuid.NewString()
		}
		w.Header().Set(HeaderRequestID, reqID)
		ctx := context.WithValue(r.Context(), RequestIDKey, reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// responseWriterInterceptor intercepts the response to record status code and bytes written.
type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int
}

func (rw *responseWriterInterceptor) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriterInterceptor) Write(b []byte) (int, error) {
	if rw.statusCode == 0 {
		rw.statusCode = http.StatusOK
	}
	n, err := rw.ResponseWriter.Write(b)
	rw.bytesWritten += n
	return n, err
}

func (rw *responseWriterInterceptor) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := rw.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("underlying ResponseWriter does not implement http.Hijacker")
}

func (rw *responseWriterInterceptor) Flush() {
	if fl, ok := rw.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}

// AccessLog emits a structured log line for every completed HTTP request.
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := GetRequestID(r.Context())

			wrapped := &responseWriterInterceptor{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			next.ServeHTTP(wrapped, r)

			duration := time.Since(start)

			logger.Info("http_request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", wrapped.statusCode),
				slog.Int64("duration_ms", duration.Milliseconds()),
				slog.Int("bytes", wrapped.bytesWritten),
				slog.String("remote_addr", r.RemoteAddr),
				slog.String("request_id", reqID),
				slog.String("user_agent", r.UserAgent()),
			)
		})
	}
}

// Recoverer recovers from panics, logs stack trace, and sends a standard JSON error response.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rvr := recover(); rvr != nil {
					reqID := GetRequestID(r.Context())
					stack := string(debug.Stack())

					metrics.Default().AppErrors.WithLabelValues("panic").Inc()
					logger.Error("panic_recovered",
						slog.Any("error", rvr),
						slog.String("request_id", reqID),
						slog.String("path", r.URL.Path),
						slog.String("stack", stack),
					)

					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(w).Encode(map[string]any{
						"error": map[string]string{
							"code":    "INTERNAL_SERVER_ERROR",
							"message": fmt.Sprintf("Unexpected panic: %v", rvr),
						},
					})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// CORS returns CORS middleware configured for the specified allowed origins.
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	return cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Request-ID", "X-Admin-Key"},
		ExposedHeaders:   []string{"Link", HeaderRequestID},
		AllowCredentials: true,
		MaxAge:           300,
	})
}

// Timeout wraps the request context with a timeout, skipping long-lived WebSocket connections.
func Timeout(timeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
				next.ServeHTTP(w, r)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// InstanceHeader stamps every response with X-Instance, the ID of the node that
// served it. Nginx copies it into X-Served-By, so the load balancer's
// distribution can be measured from outside.
func InstanceHeader(instanceID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Instance", instanceID)
			next.ServeHTTP(w, r)
		})
	}
}
