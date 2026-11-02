package remote

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	// ErrAgentOffline means the child is not connected to the hub right now.
	ErrAgentOffline = errors.New("agent is offline (not connected to this parent)")
	// ErrRelayTimeout means the child did not answer in time.
	ErrRelayTimeout = errors.New("agent did not answer in time")
	// ErrAgentReadOnly means the parent only has read access to the child.
	ErrAgentReadOnly = errors.New("agent was enrolled read-only; mutations are refused")
)

// HubConfig configures a Hub.
type HubConfig struct {
	State  *HubState
	Label  string      // shown to children as the parent's name (default: hostname)
	Logger *log.Logger // nil discards logs
	// MaxAuthFailures per IP before exponential lockout (default 5).
	MaxAuthFailures int
	PollTimeout     time.Duration // long-poll hold time (default 25s)
	RelayTimeout    time.Duration // wait for a child's answer (default 15s)
	// OfflineAfter: a child with no open poll for this long is offline (default 10s).
	OfflineAfter time.Duration
	now          func() time.Time
}

// Hub relays allow-listed profile operations from the local parent CLI to
// enrolled children over their outbound long-poll connections.
type Hub struct {
	cfg     HubConfig
	state   *HubState
	log     *log.Logger
	limiter *authLimiter
	now     func() time.Time

	mu    sync.Mutex
	conns map[string]*agentConn
	jobs  map[string]*pendingJob
	seen  map[string]time.Time
}

const revokeCheckInterval = 2 * time.Second

type agentConn struct {
	queue    chan *pendingJob
	polling  int
	lastPoll time.Time
}

type pendingJob struct {
	job   Job
	agent string
	done  chan Result
}

// NewHub validates cfg and returns a Hub.
func NewHub(cfg HubConfig) (*Hub, error) {
	if cfg.State == nil {
		return nil, errors.New("remote: nil hub state")
	}
	if cfg.Logger == nil {
		cfg.Logger = discardLogger()
	}
	if cfg.MaxAuthFailures <= 0 {
		cfg.MaxAuthFailures = 5
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 25 * time.Second
	}
	if cfg.RelayTimeout <= 0 {
		cfg.RelayTimeout = 15 * time.Second
	}
	if cfg.OfflineAfter <= 0 {
		cfg.OfflineAfter = 10 * time.Second
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.Label == "" {
		cfg.Label, _ = os.Hostname()
	}
	return &Hub{
		cfg:     cfg,
		state:   cfg.State,
		log:     cfg.Logger,
		limiter: newAuthLimiter(cfg.MaxAuthFailures, cfg.now),
		now:     cfg.now,
		conns:   map[string]*agentConn{},
		jobs:    map[string]*pendingJob{},
		seen:    cfg.State.LastSeen(),
	}, nil
}

// PublicHandler serves the network-facing API used by children.
func (h *Hub) PublicHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/enroll", h.enroll)
	mux.HandleFunc("GET /v1/agent/poll", h.withAgent(h.poll))
	mux.HandleFunc("POST /v1/agent/result/{id}", h.withAgent(h.result))
	mux.HandleFunc("GET /v1/agent/ping", h.withAgent(h.ping))
	mux.HandleFunc("POST /v1/agent/leave", h.withAgent(h.leave))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	return mux
}

func (h *Hub) rateLimited(w http.ResponseWriter, ip string) bool {
	if wait := h.limiter.blocked(ip); wait > 0 {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many requests")
		return true
	}
	return false
}

type enrollRequest struct {
	Secret string `json:"secret"`
	Name   string `json:"name,omitempty"`
}

// EnrollResponse is returned once to a child that enrolled successfully.
type EnrollResponse struct {
	Name       string `json:"name"`
	Role       Role   `json:"role"`
	Credential string `json:"credential"`
	Parent     string `json:"parent"`
}

func (h *Hub) enroll(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if h.rateLimited(w, ip) {
		return
	}
	var req enrollRequest
	if !decodeBody(w, r, &req) {
		h.limiter.fail(ip)
		return
	}
	rec, cred, err := h.state.Enroll(req.Secret, req.Name)
	switch {
	case errors.Is(err, ErrUnauthorized):
		h.limiter.fail(ip)
		h.log.Printf("enroll failure from %s", ip)
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	case errors.Is(err, ErrNameTaken):
		writeError(w, http.StatusConflict, "agent name already in use; join with another --name")
		return
	case err != nil:
		h.log.Printf("enroll error from %s: %v", ip, err)
		writeError(w, http.StatusBadRequest, "enrollment failed")
		return
	}
	h.limiter.success(ip)
	h.log.Printf("agent=%s enrolled (role %s) from %s", rec.Name, rec.Role, ip)
	writeJSON(w, http.StatusOK, EnrollResponse{Name: rec.Name, Role: rec.Role, Credential: cred, Parent: h.cfg.Label})
}

type agentHandler func(w http.ResponseWriter, r *http.Request, a AgentRecord)

func (h *Hub) withAgent(next agentHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := clientIP(r)
		if h.rateLimited(w, ip) {
			return
		}
		a, err := h.state.AuthenticateAgent(bearer(r))
		if err != nil {
			if !errors.Is(err, ErrUnauthorized) {
				h.log.Printf("agents file unreadable, denying: %v", err)
			}
			h.limiter.fail(ip)
			h.log.Printf("auth failure from %s %s", ip, r.URL.EscapedPath())
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h.limiter.success(ip)
		h.touch(a.Name)
		next(w, r, a)
	}
}

func (h *Hub) touch(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.now().UTC()
	if prev, ok := h.seen[name]; ok && now.Sub(prev) < time.Minute {
		return
	}
	h.seen[name] = now
	cp := make(map[string]time.Time, len(h.seen))
	for k, v := range h.seen {
		cp[k] = v
	}
	if err := h.state.writeSeen(cp); err != nil {
		h.log.Printf("could not record lastSeen: %v", err)
	}
}

func (h *Hub) conn(name string) *agentConn {
	c := h.conns[name]
	if c == nil {
		c = &agentConn{queue: make(chan *pendingJob, 8)}
		h.conns[name] = c
	}
	return c
}

func (h *Hub) poll(w http.ResponseWriter, r *http.Request, a AgentRecord) {
	cred := bearer(r)
	h.mu.Lock()
	c := h.conn(a.Name)
	c.polling++
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		c.polling--
		c.lastPoll = h.now()
		h.mu.Unlock()
	}()
	timer := time.NewTimer(h.cfg.PollTimeout)
	defer timer.Stop()
	// A revoke made by the CLI (file change) ends an open long-poll quickly.
	recheck := time.NewTicker(revokeCheckInterval)
	defer recheck.Stop()
	for {
		select {
		case <-recheck.C:
			if cur, err := h.state.AuthenticateAgent(cred); err != nil || cur.Name != a.Name {
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			continue
		case pj := <-c.queue:
			h.mu.Lock()
			_, live := h.jobs[pj.job.ID]
			h.mu.Unlock()
			if !live {
				continue // the waiting parent gave up
			}
			// Re-check the credential: a revoke during the long-poll wins.
			if cur, err := h.state.AuthenticateAgent(cred); err != nil || cur.Name != a.Name {
				h.finish(pj, Result{Error: "agent revoked"})
				writeError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			writeJSON(w, http.StatusOK, pj.job)
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (h *Hub) finish(pj *pendingJob, res Result) {
	h.mu.Lock()
	delete(h.jobs, pj.job.ID)
	h.mu.Unlock()
	select {
	case pj.done <- res:
	default:
	}
}

func (h *Hub) result(w http.ResponseWriter, r *http.Request, a AgentRecord) {
	id := r.PathValue("id")
	var res Result
	if !decodeBody(w, r, &res) {
		return
	}
	h.mu.Lock()
	pj, ok := h.jobs[id]
	if ok && pj.agent != a.Name {
		ok = false // a child can only answer its own jobs
	}
	h.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "unknown job")
		return
	}
	h.log.Printf("agent=%s op=%s profile=%q ok=%v", a.Name, pj.job.Op, pj.job.Args.Profile, res.OK)
	h.finish(pj, res)
	w.WriteHeader(http.StatusNoContent)
}

// AgentInfo is what /v1/agent/ping returns to a child.
type AgentInfo struct {
	Name   string `json:"name"`
	Role   Role   `json:"role"`
	Parent string `json:"parent"`
}

func (h *Hub) ping(w http.ResponseWriter, r *http.Request, a AgentRecord) {
	writeJSON(w, http.StatusOK, AgentInfo{Name: a.Name, Role: a.Role, Parent: h.cfg.Label})
}

func (h *Hub) leave(w http.ResponseWriter, r *http.Request, a AgentRecord) {
	if err := h.state.Revoke(a.Name); err != nil && !errors.Is(err, ErrAgentNotFound) {
		h.log.Printf("agent=%s leave failed: %v", a.Name, err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	h.log.Printf("agent=%s left", a.Name)
	w.WriteHeader(http.StatusNoContent)
}

// AgentStatus is one row of the admin agent listing.
type AgentStatus struct {
	Name       string     `json:"name"`
	Role       Role       `json:"role"`
	EnrolledAt time.Time  `json:"enrolledAt"`
	LastSeen   *time.Time `json:"lastSeen,omitempty"`
	Online     bool       `json:"online"`
}

// Status lists enrolled agents with their connection state.
func (h *Hub) Status() ([]AgentStatus, error) {
	agents, err := h.state.Agents()
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]AgentStatus, 0, len(agents))
	for _, a := range agents {
		s := AgentStatus{Name: a.Name, Role: a.Role, EnrolledAt: a.EnrolledAt, Online: h.onlineLocked(a.Name)}
		if t, ok := h.seen[a.Name]; ok && !t.Before(a.EnrolledAt) {
			t := t
			s.LastSeen = &t
		}
		out = append(out, s)
	}
	return out, nil
}

func (h *Hub) onlineLocked(name string) bool {
	c := h.conns[name]
	return c != nil && (c.polling > 0 || h.now().Sub(c.lastPoll) < h.cfg.OfflineAfter)
}

// Relay sends job to the named agent and waits for its answer.
func (h *Hub) Relay(ctx context.Context, agent string, job Job) (Result, error) {
	if err := job.Validate(); err != nil {
		return Result{}, err
	}
	rec, err := h.state.Agent(agent)
	if err != nil {
		return Result{}, err
	}
	if job.Mutates() && rec.Role != RoleReadWrite {
		return Result{}, ErrAgentReadOnly
	}
	job.ID = randomID(16)
	pj := &pendingJob{job: job, agent: rec.Name, done: make(chan Result, 1)}
	h.mu.Lock()
	if !h.onlineLocked(rec.Name) {
		h.mu.Unlock()
		return Result{}, ErrAgentOffline
	}
	c := h.conn(rec.Name)
	h.jobs[job.ID] = pj
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.jobs, job.ID)
		h.mu.Unlock()
	}()
	select {
	case c.queue <- pj:
	default:
		return Result{}, errors.New("agent is busy, try again")
	}
	timer := time.NewTimer(h.cfg.RelayTimeout)
	defer timer.Stop()
	select {
	case res := <-pj.done:
		return res, nil
	case <-timer.C:
		return Result{}, ErrRelayTimeout
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
}

// AdminHandler serves the loopback-only admin API used by the local parent
// CLI. Requests must come from a loopback address and carry adminToken.
func (h *Hub) AdminHandler(adminToken string) http.Handler {
	want := sha256.Sum256([]byte(adminToken))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/v1/agents", func(w http.ResponseWriter, r *http.Request) {
		st, err := h.Status()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("POST /admin/v1/relay", func(w http.ResponseWriter, r *http.Request) {
		var req relayRequest
		if !decodeBody(w, r, &req) {
			return
		}
		res, err := h.Relay(r.Context(), req.Agent, req.Job)
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, res)
		case errors.Is(err, ErrAgentNotFound):
			writeError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, ErrAgentOffline):
			writeError(w, http.StatusConflict, err.Error())
		case errors.Is(err, ErrAgentReadOnly):
			writeError(w, http.StatusForbidden, err.Error())
		case errors.Is(err, ErrRelayTimeout):
			writeError(w, http.StatusGatewayTimeout, err.Error())
		default:
			writeError(w, http.StatusBadRequest, err.Error())
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ip := net.ParseIP(clientIP(r)); ip == nil || !ip.IsLoopback() {
			writeError(w, http.StatusForbidden, "forbidden")
			return
		}
		got := sha256.Sum256([]byte(bearer(r)))
		if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

type relayRequest struct {
	Agent string `json:"agent"`
	Job   Job    `json:"job"`
}
