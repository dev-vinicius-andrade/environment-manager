package parent

import (
	ctx "context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"

	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

// parent serve [--addr 127.0.0.1:7777]
func createServeCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	var addr string
	command := newLeaf(parent, "serve", "Run the hub that children connect to (foreground; Ctrl+C stops)", 0,
		func(cmd *cobra.Command, args []string) error {
			stderr := cmd.ErrOrStderr()
			if _, _, err := net.SplitHostPort(addr); err != nil {
				return fmt.Errorf("invalid --addr %q: %w", addr, err)
			}
			if !agent.IsLoopbackAddr(addr) {
				fmt.Fprintf(stderr, "WARNING: listening on non-loopback address %s. Anyone who can reach it can try join codes;\n"+
					"         restrict it with a firewall. Children need only outbound access to this port.\n", addr)
			}
			st, err := hubState()
			if err != nil {
				return err
			}
			if ai, err := agent.ReadAdminInfo(st.Dir()); err == nil {
				if c, err := agent.NewAdminClient(st.Dir()); err == nil {
					if _, err := c.Agents(); err == nil {
						return fmt.Errorf("a hub is already running (pid %d, %s)", ai.PID, ai.PublicAddr)
					}
				}
			}
			rh, err := agent.StartHub(agent.HubConfig{State: st, Logger: agent.NewHubLogger(stderr)}, addr)
			if err != nil {
				return err
			}
			defer rh.Close()
			fmt.Fprintf(stderr, "envmgr parent hub listening on https://%s (profile operations only)\n", rh.PublicAddr)
			fmt.Fprintf(stderr, "certificate fingerprint (SHA-256): %s\n", rh.Identity.Fingerprint)
			fmt.Fprintf(stderr, "local admin API on https://%s (loopback only; credentials in %s)\n", rh.AdminAddr, st.Dir())
			if agent.IsLoopbackAddr(rh.PublicAddr) {
				fmt.Fprintln(stderr, "note: only children on this machine (or through a tunnel) can reach a loopback address; use --addr <lan-ip>:7777 for other machines")
			}
			fmt.Fprintln(stderr, "invite a child with: envmgr parent invite --name <child>")

			sigCtx, stop := signal.NotifyContext(ctx.Background(), os.Interrupt)
			defer stop()
			select {
			case err := <-rh.Done():
				if errors.Is(err, http.ErrServerClosed) {
					return nil
				}
				return err
			case <-sigCtx.Done():
				fmt.Fprintln(stderr, "shutting down")
				return nil
			}
		})
	command.Flags().StringVar(&addr, "addr", agent.DefaultAddr, "listen address host:port (non-loopback prints a warning)")
	return command
}
