package parent

import (
	"errors"
	"fmt"
	"net"
	"text/tabwriter"
	"time"

	agent "github.com/dev-vinicius-andrade/environment-manager/internal/remote"
	"github.com/dev-vinicius-andrade/environment-manager/types/context"
	"github.com/spf13/cobra"
)

const timeFmt = "2006-01-02 15:04"

func fmtTime(t time.Time) string { return t.Local().Format(timeFmt) }

// parent invite [--name n] [--expires 15m] [--read-only] [--addr-public host:port]
func createInviteCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	var name, addrPublic string
	var expires time.Duration
	var readOnly bool
	command := newLeaf(parent, "invite", "Create a single-use, expiring join code for a child", 0,
		func(cmd *cobra.Command, args []string) error {
			st, err := hubState()
			if err != nil {
				return err
			}
			id, err := agent.LoadOrCreateIdentity(st.Dir())
			if err != nil {
				return err
			}
			if addrPublic == "" {
				ai, err := agent.ReadAdminInfo(st.Dir())
				if err != nil {
					return errors.New("cannot tell which address children should dial: start `envmgr parent serve` first or pass --addr-public host:port")
				}
				addrPublic = ai.PublicAddr
			}
			if host, _, err := net.SplitHostPort(addrPublic); err == nil {
				if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
					return fmt.Errorf("the hub listens on %s, which a child cannot dial; pass --addr-public <this-machine-ip>:<port>", addrPublic)
				}
			}
			role := agent.RoleReadWrite
			if readOnly {
				role = agent.RoleReadOnly
			}
			code, inv, err := agent.CreateJoinCode(st, id.Fingerprint, addrPublic, name, role, expires)
			if err != nil {
				return err
			}
			stderr := cmd.ErrOrStderr()
			fmt.Fprintf(stderr, "invite %q (role %s) for %s, single use, expires %s\n", inv.Name, inv.Role, addrPublic, fmtTime(inv.ExpiresAt))
			if agent.IsLoopbackAddr(addrPublic) {
				fmt.Fprintln(stderr, "note: the address is loopback; only a child on this machine (or through a tunnel) can use it")
			}
			fmt.Fprintln(stderr, "run this on the child (treat it like a password until it is used or expires):")
			fmt.Fprintf(cmd.OutOrStdout(), "envmgr join %s\n", code)
			return nil
		})
	f := command.Flags()
	f.StringVar(&name, "name", "", "agent name for the child (default: generated; the child may then propose its own with join --name)")
	f.DurationVar(&expires, "expires", 15*time.Minute, "how long the code stays valid")
	f.BoolVar(&readOnly, "read-only", false, "this parent may only read the child's profiles")
	f.StringVar(&addrPublic, "addr-public", "", "host:port the child dials (default: the running hub's listen address)")
	return command
}

// parent invites
func createInvitesCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "invites", "List pending (unused, unexpired) invites", 0,
		func(cmd *cobra.Command, args []string) error {
			st, err := hubState()
			if err != nil {
				return err
			}
			invs, err := st.Invites()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tROLE\tCREATED\tEXPIRES")
			for _, i := range invs {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", i.Name, i.Role, fmtTime(i.CreatedAt), fmtTime(i.ExpiresAt))
			}
			return tw.Flush()
		})
}

// parent cancel <invite-name>
func createCancelCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "cancel <invite-name>", "Cancel a pending invite", 1,
		func(cmd *cobra.Command, args []string) error {
			st, err := hubState()
			if err != nil {
				return err
			}
			return st.CancelInvite(args[0])
		})
}

// parent agents
func createAgentsCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "agents", "List enrolled children", 0,
		func(cmd *cobra.Command, args []string) error {
			var rows []agent.AgentStatus
			hubDown := false
			if c, err := adminClient(); err == nil {
				rows, err = c.Agents()
				if err != nil {
					hubDown = true
				}
			} else {
				hubDown = true
			}
			if hubDown {
				st, err := hubState()
				if err != nil {
					return err
				}
				recs, err := st.Agents()
				if err != nil {
					return err
				}
				seen := st.LastSeen()
				rows = rows[:0]
				for _, r := range recs {
					s := agent.AgentStatus{Name: r.Name, Role: r.Role, EnrolledAt: r.EnrolledAt}
					if t, ok := seen[r.Name]; ok && !t.Before(r.EnrolledAt) {
						s.LastSeen = &t
					}
					rows = append(rows, s)
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "hub not running: every child is offline")
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tROLE\tENROLLED\tLAST SEEN\tONLINE")
			for _, s := range rows {
				last, online := "never", "no"
				if s.LastSeen != nil {
					last = fmtTime(*s.LastSeen)
				}
				if s.Online {
					online = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", s.Name, s.Role, fmtTime(s.EnrolledAt), last, online)
			}
			return tw.Flush()
		})
}

// parent revoke <agent>
func createRevokeCommand(_ *context.EnvContext, parent *cobra.Command) *cobra.Command {
	return newLeaf(parent, "revoke <agent>", "Remove a child; its credential stops working on its next request", 1,
		func(cmd *cobra.Command, args []string) error {
			st, err := hubState()
			if err != nil {
				return err
			}
			if err := st.Revoke(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "revoked %q; it must join again with a new code to reconnect\n", args[0])
			return nil
		})
}
