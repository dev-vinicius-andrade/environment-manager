package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const adminFile = "admin.json"

// ErrHubNotRunning means no running `envmgr parent serve` was found.
var ErrHubNotRunning = errors.New("the parent hub is not running (start it with `envmgr parent serve`)")

// AdminInfo is written to <parent dir>/admin.json (0600) by a running hub so
// the local parent CLI can reach its loopback admin API. The admin token is
// regenerated on every start and the file is removed on clean shutdown.
type AdminInfo struct {
	Addr        string `json:"addr"`        // loopback admin listener
	Token       string `json:"token"`       // admin bearer token (local admin only)
	Fingerprint string `json:"fingerprint"` // hub certificate, pinned by the CLI
	PublicAddr  string `json:"publicAddr"`  // where children connect (default for invites)
	PID         int    `json:"pid"`
}

// ReadAdminInfo reads admin.json from dir.
func ReadAdminInfo(dir string) (*AdminInfo, error) {
	b, err := os.ReadFile(filepath.Join(dir, adminFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrHubNotRunning
	}
	if err != nil {
		return nil, err
	}
	var ai AdminInfo
	if err := json.Unmarshal(b, &ai); err != nil {
		return nil, fmt.Errorf("%s: %w", adminFile, err)
	}
	return &ai, nil
}

// RunningHub is a started hub: public TLS listener plus loopback admin listener.
type RunningHub struct {
	Hub         *Hub
	Identity    *Identity
	PublicAddr  string
	AdminAddr   string
	adminToken  string
	dir         string
	public      *http.Server
	admin       *http.Server
	publicErr   chan error
	adminClosed chan error
}

// StartHub loads/creates the identity in cfg.State's dir, listens on addr
// (public, TLS) and on 127.0.0.1:0 (admin, TLS, loopback only), and writes
// admin.json. Call Close to stop and remove admin.json.
func StartHub(cfg HubConfig, addr string) (*RunningHub, error) {
	if cfg.State == nil {
		return nil, errors.New("remote: nil hub state")
	}
	dir := cfg.State.Dir()
	id, err := LoadOrCreateIdentity(dir)
	if err != nil {
		return nil, err
	}
	hub, err := NewHub(cfg)
	if err != nil {
		return nil, err
	}
	token, err := generateSecret()
	if err != nil {
		return nil, err
	}
	pubLn, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	admLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		pubLn.Close()
		return nil, err
	}
	rh := &RunningHub{
		Hub: hub, Identity: id, dir: dir, adminToken: token,
		PublicAddr: pubLn.Addr().String(), AdminAddr: admLn.Addr().String(),
		public:    newTLSServer(hub.PublicHandler(), id.Cert),
		admin:     newTLSServer(hub.AdminHandler(token), id.Cert),
		publicErr: make(chan error, 1), adminClosed: make(chan error, 1),
	}
	info := AdminInfo{Addr: rh.AdminAddr, Token: token, Fingerprint: id.Fingerprint, PublicAddr: rh.PublicAddr, PID: os.Getpid()}
	if err := writeJSONFile(filepath.Join(dir, adminFile), info); err != nil {
		pubLn.Close()
		admLn.Close()
		return nil, err
	}
	go func() { rh.publicErr <- rh.public.ServeTLS(pubLn, "", "") }()
	go func() { rh.adminClosed <- rh.admin.ServeTLS(admLn, "", "") }()
	return rh, nil
}

// Done returns a channel that receives the public listener's terminal error.
func (rh *RunningHub) Done() <-chan error { return rh.publicErr }

// Close shuts both listeners down and removes admin.json.
// Long-polls and in-flight relays are cut immediately (children reconnect).
func (rh *RunningHub) Close() error {
	// Remove admin.json only if it is still ours (another hub may have replaced it).
	if ai, err := ReadAdminInfo(rh.dir); err == nil && ai.Token == rh.adminToken {
		_ = os.Remove(filepath.Join(rh.dir, adminFile))
	}
	_ = rh.admin.Close()
	return rh.public.Close()
}

// AdminClient talks to a running hub's loopback admin API.
type AdminClient struct {
	info *AdminInfo
	http *http.Client
}

// NewAdminClient reads admin.json in dir and returns a pinned admin client.
func NewAdminClient(dir string) (*AdminClient, error) {
	ai, err := ReadAdminInfo(dir)
	if err != nil {
		return nil, err
	}
	if !IsLoopbackAddr(ai.Addr) {
		return nil, fmt.Errorf("%s: admin address %q is not loopback", adminFile, ai.Addr)
	}
	c, err := pinnedClient(ai.Fingerprint, 30*time.Second)
	if err != nil {
		return nil, err
	}
	return &AdminClient{info: ai, http: c}, nil
}

// Info returns the hub's admin.json contents.
func (a *AdminClient) Info() AdminInfo { return *a.info }

func (a *AdminClient) do(method, path string, in, out any) error {
	_, err := doJSON(context.Background(), a.http, method, "https://"+a.info.Addr+path, a.info.Token, in, out)
	var ne *net.OpError
	if errors.As(err, &ne) && (errors.Is(err, syscall.ECONNREFUSED) || ne.Op == "dial") {
		return ErrHubNotRunning
	}
	return err
}

// Agents lists enrolled agents with online state.
func (a *AdminClient) Agents() ([]AgentStatus, error) {
	var out []AgentStatus
	return out, a.do(http.MethodGet, "/admin/v1/agents", nil, &out)
}

// Relay runs job on agent through the hub. A Result with OK=false carries the
// child's error message.
func (a *AdminClient) Relay(agent string, job Job) (Result, error) {
	var res Result
	err := a.do(http.MethodPost, "/admin/v1/relay", relayRequest{Agent: agent, Job: job}, &res)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Message != "" {
			return Result{}, errors.New(ae.Message)
		}
		return Result{}, err
	}
	return res, nil
}

// CreateJoinCode stores a single-use invite in st and returns the join code
// for a child (publicAddr must be reachable from the child).
func CreateJoinCode(st *HubState, fingerprint, publicAddr, name string, role Role, ttl time.Duration) (string, Invite, error) {
	if err := ValidAddress(publicAddr); err != nil {
		return "", Invite{}, err
	}
	fp, err := NormalizeFingerprint(fingerprint)
	if err != nil {
		return "", Invite{}, err
	}
	secret, inv, err := st.CreateInvite(name, role, ttl)
	if err != nil {
		return "", Invite{}, err
	}
	jc := JoinCode{Addr: publicAddr, FP: fp, Secret: secret, Exp: inv.ExpiresAt.Unix()}
	if inv.NameFixed {
		jc.Name = inv.Name
	}
	return jc.Encode(), inv, nil
}

// NewHubLogger returns a logger with the hub prefix.
func NewHubLogger(w interface{ Write([]byte) (int, error) }) *log.Logger {
	return log.New(w, "envmgr parent: ", log.LstdFlags)
}
