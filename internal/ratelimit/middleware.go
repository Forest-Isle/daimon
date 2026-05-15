package ratelimit

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// RateLimitMiddleware returns an HTTP middleware that rate-limits requests
// using the provided Limiter.
//
// keyFunc extracts a key from the request for per-key rate limiting (e.g., IP
// address or X-User-ID header). If keyFunc is nil, req.RemoteAddr is used.
//
// When a request exceeds the limit, the middleware responds with HTTP 429 Too
// Many Requests and includes a Retry-After header.
func RateLimitMiddleware(limiter Limiter, keyFunc func(*http.Request) string) func(http.Handler) http.Handler {
	if keyFunc == nil {
		keyFunc = defaultKeyFunc
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := keyFunc(r)
			allowed, waitTime, err := limiter.Allow(r.Context(), key)
			if err != nil {
				slog.Warn("ratelimit: limiter error, allowing request", "err", err, "key", key)
				next.ServeHTTP(w, r)
				return
			}

			// Add rate limit headers
			w.Header().Set("X-RateLimit-Limit-Key", key)
			if !allowed {
				retryAfter := int64(waitTime.Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				w.Header().Set("Retry-After", strconv.FormatInt(retryAfter, 10))
				w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Unix()+retryAfter, 10))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			w.Header().Set("X-RateLimit-Remaining", "1")
			next.ServeHTTP(w, r)
		})
	}
}

func defaultKeyFunc(r *http.Request) string {
	return r.RemoteAddr
}

// HeaderKeyFunc returns a key function that extracts the rate limit key from
// the given header. Falls back to RemoteAddr if the header is empty.
func HeaderKeyFunc(header string) func(*http.Request) string {
	return func(r *http.Request) string {
		if v := r.Header.Get(header); v != "" {
			return v
		}
		return r.RemoteAddr
	}
}

// IPKeyFunc is a key function that uses the client IP address.
func IPKeyFunc(r *http.Request) string {
	return r.RemoteAddr
}
