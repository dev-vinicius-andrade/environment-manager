package remote

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
)

const (
	// MaxBodyBytes is the largest request body the server accepts.
	MaxBodyBytes = 1 << 20
	// DefaultAddr is the default listen address (loopback only).
	DefaultAddr = "127.0.0.1:7777"
)

// Config configures a Server.
type Config struct {
	Store    *profile.Store
	Token    string
	ReadOnly bool
	Logger   *log.Logger // nil discards logs
	// MaxAuthFailures is how many failed auth attempts an IP may make before
	// it is locked out with exponential backoff (default 5).
	MaxAuthFailures int
	now             func() time.Time
}

// Server serves the profile API. Construct with NewServer.
type Server struct {
	store     *profile.Store
	tokenHash [32]byte
	readOnly  bool
	log       *log.Logger
	limiter   *authLimiter
	mu        sync.RWMutex // serializes store mutations
}

// NewServer validates cfg and returns a Server.
func NewServer(cfg Config) (*Server, error) {
	if cfg.Store == nil {
		return nil, errors.New("remote: nil store")
	}
	if !validToken(cfg.Token) {
		return nil, errors.New("remote: invalid token")
	}
	lg := cfg.Logger
	if lg == nil {
		lg = log.New(io.Discard, "", 0)
	}
	if cfg.MaxAuthFailures <= 0 {
		cfg.MaxAuthFailures = 5
	}
	now := cfg.now
	if now == nil {
		now = time.Now
	}
	return &Server{
		store:     cfg.Store,
		tokenHash: sha256.Sum256([]byte(cfg.Token)),
		readOnly:  cfg.ReadOnly,
		log:       lg,
		limiter:   newAuthLimiter(cfg.MaxAuthFailures, now),
	}, nil
}

// NewHTTPServer wraps h in an *http.Server with TLS 1.3 using cert and
// conservative timeouts. Call ServeTLS(listener, "", "") on it.
func NewHTTPServer(h http.Handler, cert tls.Certificate) *http.Server {
	return &http.Server{
		Handler: h,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS13,
		},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
}

// Handler returns the authenticated API handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/profiles", s.listProfiles)
	mux.HandleFunc("GET /v1/profiles/{name}", s.getProfile)
	mux.HandleFunc("PUT /v1/profiles/{name}", s.putProfile)
	mux.HandleFunc("DELETE /v1/profiles/{name}", s.deleteProfile)
	mux.HandleFunc("PUT /v1/profiles/{name}/vars/{var}", s.setVar)
	mux.HandleFunc("DELETE /v1/profiles/{name}/vars/{var}", s.unsetVar)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	return s.withAuth(s.withReadOnly(mux))
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if wait := s.limiter.blocked(ip); wait > 0 {
			w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
			writeError(w, http.StatusTooManyRequests, "too many requests")
			return
		}
		if !s.authorized(r) {
			s.limiter.fail(ip)
			s.log.Printf("auth failure from %s %s %s", ip, r.Method, r.URL.EscapedPath())
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		s.limiter.success(ip)
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return false
	}
	got := sha256.Sum256([]byte(h[len(prefix):]))
	return subtle.ConstantTimeCompare(got[:], s.tokenHash[:]) == 1
}

func (s *Server) withReadOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.readOnly && r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusForbidden, "server is read-only")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	names, err := s.store.List()
	s.mu.RUnlock()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, listResponse{Profiles: names})
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r)
	if !ok {
		return
	}
	s.mu.RLock()
	p, err := s.store.Get(name)
	s.mu.RUnlock()
	if errors.Is(err, profile.ErrNotFound) {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) putProfile(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r)
	if !ok {
		return
	}
	var p profile.Profile
	if !decodeBody(w, r, &p) {
		return
	}
	if p.Name != "" && p.Name != name {
		writeError(w, http.StatusBadRequest, "profile name in body does not match path")
		return
	}
	p.Name = name
	if p.Vars == nil {
		p.Vars = map[string]string{}
	}
	if err := p.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.mu.Lock()
	err := s.store.Save(p)
	s.mu.Unlock()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.log.Printf("put profile %q (%d vars) from %s", name, len(p.Vars), clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	err := s.store.Delete(name)
	s.mu.Unlock()
	if errors.Is(err, profile.ErrNotFound) {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.log.Printf("delete profile %q from %s", name, clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setVar(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r)
	if !ok {
		return
	}
	key, ok := pathVar(w, r)
	if !ok {
		return
	}
	var body varRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Value == nil {
		writeError(w, http.StatusBadRequest, `missing "value"`)
		return
	}
	s.mu.Lock()
	err := s.store.SetVar(name, key, *body.Value)
	s.mu.Unlock()
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.log.Printf("set %s in profile %q from %s", key, name, clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) unsetVar(w http.ResponseWriter, r *http.Request) {
	name, ok := pathName(w, r)
	if !ok {
		return
	}
	key, ok := pathVar(w, r)
	if !ok {
		return
	}
	s.mu.Lock()
	err := s.store.UnsetVar(name, key)
	s.mu.Unlock()
	if errors.Is(err, profile.ErrNotFound) {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.log.Printf("unset %s in profile %q from %s", key, name, clientIP(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	// Store errors carry paths and names, never values.
	s.log.Printf("internal error on %s %s: %v", r.Method, r.URL.EscapedPath(), err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func pathName(w http.ResponseWriter, r *http.Request) (string, bool) {
	n := r.PathValue("name")
	if !profile.ValidName(n) {
		writeError(w, http.StatusBadRequest, "invalid profile name")
		return "", false
	}
	return n, true
}

func pathVar(w http.ResponseWriter, r *http.Request) (string, bool) {
	v := r.PathValue("var")
	if !profile.ValidVar(v) {
		writeError(w, http.StatusBadRequest, "invalid variable name")
		return "", false
	}
	return v, true
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(dst)
	if err == nil && dec.More() {
		err = errors.New("trailing data")
	}
	if err == nil {
		// Drain to detect trailing junk or an oversized body.
		_, err = io.Copy(io.Discard, r.Body)
	}
	var mbe *http.MaxBytesError
	switch {
	case errors.As(err, &mbe):
		writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
		return false
	case err != nil:
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type listResponse struct {
	Profiles []string `json:"profiles"`
}

type varRequest struct {
	Value *string `json:"value"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// authLimiter locks out an IP after max consecutive auth failures, doubling
// the lockout on each further failure (1s, 2s, 4s ... capped at 5 minutes).
type authLimiter struct {
	mu      sync.Mutex
	max     int
	now     func() time.Time
	entries map[string]*authEntry
}

type authEntry struct {
	failures    int
	lockedUntil time.Time
	last        time.Time
}

const (
	maxLockout      = 5 * time.Minute
	limiterEntryTTL = time.Hour
	limiterMaxSize  = 10000
)

func newAuthLimiter(max int, now func() time.Time) *authLimiter {
	return &authLimiter{max: max, now: now, entries: map[string]*authEntry{}}
}

func (l *authLimiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := l.entries[ip]
	if e == nil {
		return 0
	}
	if d := e.lockedUntil.Sub(l.now()); d > 0 {
		return d
	}
	return 0
}

func (l *authLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gc(now)
	e := l.entries[ip]
	if e == nil {
		e = &authEntry{}
		l.entries[ip] = e
	}
	e.failures++
	e.last = now
	if over := e.failures - l.max; over >= 0 {
		d := maxLockout
		if over < 9 {
			d = time.Second << over
		}
		if d > maxLockout {
			d = maxLockout
		}
		e.lockedUntil = now.Add(d)
	}
}

func (l *authLimiter) success(ip string) {
	l.mu.Lock()
	delete(l.entries, ip)
	l.mu.Unlock()
}

// gc drops stale entries; if still too large, it drops unlocked entries so
// memory stays bounded under a flood of source addresses.
func (l *authLimiter) gc(now time.Time) {
	if len(l.entries) < limiterMaxSize {
		return
	}
	for ip, e := range l.entries {
		if now.Sub(e.last) > limiterEntryTTL || !e.lockedUntil.After(now) {
			delete(l.entries, ip)
		}
	}
}
