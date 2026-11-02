package serve

import (
	ctx "context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/dev-vinicius-andrade/environment-manager/helpers"
	"github.com/dev-vinicius-andrade/environment-manager/internal/profile"
	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

type commandDefinition struct {
	cmd           *cobra.Command
	context       *context.EnvContext
	parentCommand *cobra.Command
	addr          string
	readOnly      bool
	showToken     bool
}

func (c *commandDefinition) runCommand(cmd *cobra.Command, args []string) error {
	stderr := cmd.ErrOrStderr()
	store, err := profile.NewStore()
	if err != nil {
		return err
	}
	dir, err := agent.DefaultDir()
	if err != nil {
		return err
	}
	id, err := agent.LoadOrCreateIdentity(dir)
	if err != nil {
		return err
	}
	if c.showToken {
		// Data on stdout so it can be captured; nothing else is started.
		fmt.Fprintf(cmd.OutOrStdout(), "token: %s\nfingerprint: %s\n", id.Token, id.Fingerprint)
		return nil
	}
	if err := agent.ValidAddress(c.addr); err != nil && !strings.HasPrefix(c.addr, ":") {
		return err
	}
	if !isLoopback(c.addr) {
		fmt.Fprintf(stderr, "WARNING: listening on non-loopback address %s; any host that can reach it can try the token.\n"+
			"         Restrict it with a firewall and prefer --read-only when writes are not needed.\n", c.addr)
	}

	srv, err := agent.NewServer(agent.Config{
		Store:    store,
		Token:    id.Token,
		ReadOnly: c.readOnly,
		Logger:   log.New(stderr, "envmgr serve: ", log.LstdFlags),
	})
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", c.addr)
	if err != nil {
		return err
	}
	hs := agent.NewHTTPServer(srv.Handler(), id.Cert)
	printBanner(stderr, ln.Addr().String(), id, c.readOnly)

	sigCtx, stop := signal.NotifyContext(ctx.Background(), os.Interrupt)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- hs.ServeTLS(ln, "", "") }()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sigCtx.Done():
		fmt.Fprintln(stderr, "shutting down")
		shutdownCtx, cancel := ctx.WithTimeout(ctx.Background(), 5*time.Second)
		defer cancel()
		return hs.Shutdown(shutdownCtx)
	}
}

func printBanner(w io.Writer, addr string, id *agent.Identity, readOnly bool) {
	mode := "read-write"
	if readOnly {
		mode = "read-only"
	}
	fmt.Fprintf(w, "envmgr serve listening on https://%s (%s, profile operations only)\n", addr, mode)
	fmt.Fprintf(w, "certificate fingerprint (SHA-256): %s\n", id.Fingerprint)
	if id.CreatedToken {
		fmt.Fprintf(w, "access token (shown once; re-print with `envmgr serve --show-token`): %s\n", id.Token)
		fmt.Fprintf(w, "pair from the controlling machine with:\n  envmgr remote add <name> <this-host>:<port> --token %s --fingerprint %s\n", id.Token, id.Fingerprint)
	} else {
		fmt.Fprintln(w, "access token: unchanged (print it with `envmgr serve --show-token`)")
	}
}

// isLoopback reports whether addr's host is a loopback IP or "localhost".
// An empty host (":7777") binds every interface and is not loopback.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (c *commandDefinition) createCommandDefinition() *cobra.Command {
	command := &cobra.Command{
		Use:   "serve",
		Short: "Run the remote agent so another machine can manage this machine's profiles",
		Long: `Run an HTTPS agent exposing this machine's profiles (list/get/put/delete, set/unset a variable).
No command execution is possible through it.

On first run a self-signed certificate and a random access token are created under
<ENVMGR_HOME>/remote/. The certificate fingerprint and the token are what the
controlling machine needs: envmgr remote add <name> <host:port> --token T --fingerprint FP

The agent binds 127.0.0.1:7777 by default. Binding another interface needs an explicit --addr.`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE:         c.runCommand,
	}
	c.cmd = command
	c.defineFlags()
	c.defineSubCommands()
	helpers.CobraHelper.AddCommandToParent(command, c.parentCommand)
	return command
}

func (c *commandDefinition) defineSubCommands() {
}

func (c *commandDefinition) defineFlags() {
	f := c.cmd.Flags()
	f.StringVar(&c.addr, "addr", agent.DefaultAddr, "listen address host:port (non-loopback addresses print a warning)")
	f.BoolVar(&c.readOnly, "read-only", false, "reject every mutating request with 403")
	f.BoolVar(&c.showToken, "show-token", false, "print the access token and certificate fingerprint, then exit")
}

// CreateCommand builds `envmgr serve` and attaches it to parentCommand.
func CreateCommand(context *context.EnvContext, parentCommand *cobra.Command) *cobra.Command {
	command := &commandDefinition{
		context:       context,
		parentCommand: parentCommand,
	}
	return command.createCommandDefinition()
}
