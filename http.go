package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ─── Gzip middleware ──────────────────────────────────────────────────────────

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		// Skip endpoints that emit binary blobs — docx/zip/images are
		// already compressed; re-gzipping wastes CPU and historically broke
		// downloads when a handler set Content-Length to the uncompressed
		// length.
		if strings.HasPrefix(r.URL.Path, "/api/note/export") ||
			strings.HasPrefix(r.URL.Path, "/api/file") ||
			strings.HasPrefix(r.URL.Path, "/api/vault-icon") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", "Accept-Encoding")
		gz, _ := gzip.NewWriterLevel(w, gzip.BestSpeed)
		defer gz.Close()
		next.ServeHTTP(&gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (g *gzipResponseWriter) Write(b []byte) (int, error)  { return g.Writer.Write(b) }
func (g *gzipResponseWriter) Header() http.Header          { return g.ResponseWriter.Header() }
func (g *gzipResponseWriter) WriteHeader(code int)         { g.ResponseWriter.WriteHeader(code) }

// ─── HTTP response helpers ────────────────────────────────────────────────────

func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("json encode: %v", err)
	}
}

func errResponse(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// originGuard rejects mutating requests whose Origin/Referer doesn't match
// the request's Host. Defends against cross-site form-POST CSRF where a
// malicious page tries to ride a logged-in user's session cookie to write
// into their vault. GET/HEAD/OPTIONS are not rate-limited or origin-checked.
// Only /api/* is wrapped: /share/<token> endpoints authenticate via the
// token in the URL and don't carry the user's cookie.
func originGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		// Only guard /api/* — share + webdav use other auth.
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		host := r.Host
		allow := func(raw string) bool {
			if raw == "" {
				return false
			}
			// Origin is "scheme://host[:port]" — strip scheme.
			if i := strings.Index(raw, "://"); i >= 0 {
				raw = raw[i+3:]
			}
			// Strip path from Referer.
			if i := strings.IndexByte(raw, '/'); i >= 0 {
				raw = raw[:i]
			}
			return raw == host
		}
		origin := r.Header.Get("Origin")
		referer := r.Header.Get("Referer")
		if origin == "" && referer == "" {
			// Same-origin server-to-server with no Origin/Referer is possible
			// (curl scripts, etc.). Allow — Authelia gates the cookie anyway.
			next.ServeHTTP(w, r)
			return
		}
		if (origin != "" && allow(origin)) || (origin == "" && allow(referer)) {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "cross-origin request blocked", http.StatusForbidden)
	})
}

// isTrustedProxyPeer returns true when the immediate TCP peer is a
// private/Docker bridge address — i.e. the request reached us through
// Traefik (or another in-cluster proxy) rather than directly from a
// random client that could spoof X-Real-IP/X-Forwarded-For.
func isTrustedProxyPeer(host string) bool {
	if host == "" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	// Strip IPv6 brackets / zone if present.
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	if i := strings.Index(host, "%"); i >= 0 {
		host = host[:i]
	}
	// Private IPv4 ranges that map to the docker bridge / VPN / LAN.
	// 10.0.0.0/8 · 172.16.0.0/12 · 192.168.0.0/16
	if strings.HasPrefix(host, "10.") || strings.HasPrefix(host, "192.168.") {
		return true
	}
	if strings.HasPrefix(host, "172.") {
		// crude check: 172.16.0.0/12 = 172.16..172.31
		var b int
		_, err := fmt.Sscanf(host[4:], "%d", &b)
		if err == nil && b >= 16 && b <= 31 {
			return true
		}
	}
	return false
}

// rateLimiter per-IP sliding window.
type rateLimiter struct {
	handler http.Handler
	mu sync.Mutex
	visitors map[string][]time.Time
	limit int
	window time.Duration
}

func newRateLimiter(h http.Handler, limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{handler: h, visitors: make(map[string][]time.Time), limit: limit, window: window}
}

func (rl *rateLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	peer := r.RemoteAddr
	if idx := strings.LastIndex(peer, ":"); idx != -1 { peer = peer[:idx] }
	ip := peer
	// Behind Traefik (only ingress path), honor forwarded client IP so the
	// per-IP bucket is real-per-user, not a single shared bridge-IP bucket.
	// But ONLY trust the headers when the immediate peer is on a private
	// Docker bridge (172.16.0.0/12 incl. 172.10.0.0/16) — otherwise anyone
	// hitting the container directly could spoof their bucket key by
	// setting X-Real-IP themselves.
	if isTrustedProxyPeer(peer) {
		if xri := r.Header.Get("X-Real-IP"); xri != "" {
			ip = xri
		} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if comma := strings.Index(xff, ","); comma != -1 {
				ip = strings.TrimSpace(xff[:comma])
			} else {
				ip = strings.TrimSpace(xff)
			}
		}
	}
	now := time.Now()
	rl.mu.Lock()
	cutoff := now.Add(-rl.window)
	var valid []time.Time
	for _, t := range rl.visitors[ip] {
		if t.After(cutoff) { valid = append(valid, t) }
	}
	if len(valid) >= rl.limit {
		rl.mu.Unlock()
		w.Header().Set("Retry-After", fmt.Sprintf("%d", int(rl.window.Seconds())))
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	rl.visitors[ip] = append(valid, now)
	rl.mu.Unlock()
	rl.handler.ServeHTTP(w, r)
}
