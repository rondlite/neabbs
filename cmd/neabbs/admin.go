package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"text/tabwriter"

	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/store"
	"github.com/rondlite/neabbs/internal/store/sqlitestore"
)

const adminUsage = `usage: neabbs admin <command> [args]

commands:
  inspect [handle]          show all players, or one in detail
  promote <handle> <0-9>    set THIS level (implies membership)
  member <handle> on|off    flip THIS membership
  ban <handle>              ban a player
  unban <handle>            lift a ban
  sysop <handle> on|off     grant/revoke sysop (in-game moderation)
  flag <handle> <flag>      grant a flag
`

// runAdmin is the offline admin CLI, operating directly on the DB file.
func runAdmin(args []string) error {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, adminUsage)
		return fmt.Errorf("missing admin command")
	}
	cfg := config.FromEnv()
	st, err := sqlitestore.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db %s: %w", cfg.DBPath, err)
	}
	defer st.Close()
	ctx := context.Background()

	byHandle := func(h string) (*store.Player, error) {
		p, err := st.PlayerByHandle(ctx, h)
		if err != nil {
			return nil, fmt.Errorf("player %q: %w", h, err)
		}
		return p, nil
	}

	switch cmd := args[0]; cmd {
	case "inspect":
		if len(args) > 1 {
			p, err := byHandle(args[1])
			if err != nil {
				return err
			}
			flags := make([]string, 0, len(p.Flags))
			for f := range p.Flags {
				flags = append(flags, f)
			}
			sort.Strings(flags)
			fmt.Printf("handle      : %s\nfingerprint : %s\nthis_member : %v\nlevel       : %d\nbanned      : %v\nsysop       : %v\nspeed       : %d\ncreated     : %s\nlast_seen   : %s\nflags       : %v\n",
				p.Handle, p.Fingerprint, p.ThisMember, p.Level, p.Banned, p.Admin, p.Speed,
				p.CreatedAt.Format("2006-01-02 15:04"), p.LastSeen.Format("2006-01-02 15:04"), flags)
			return nil
		}
		players, err := st.AllPlayers(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "HANDLE\tMEMBER\tLEVEL\tBANNED\tFLAGS\tLAST SEEN")
		for _, p := range players {
			h := p.Handle
			if h == "" {
				h = "(geen)"
			}
			fmt.Fprintf(w, "%s\t%v\t%d\t%v\t%d\t%s\n", h, p.ThisMember, p.Level, p.Banned, len(p.Flags), p.LastSeen.Format("2006-01-02 15:04"))
		}
		return w.Flush()

	case "promote":
		if len(args) != 3 {
			return fmt.Errorf("usage: neabbs admin promote <handle> <0-9>")
		}
		lvl, err := strconv.Atoi(args[2])
		if err != nil {
			return fmt.Errorf("bad level %q", args[2])
		}
		p, err := byHandle(args[1])
		if err != nil {
			return err
		}
		if err := st.SetThisMember(ctx, p.Fingerprint, true); err != nil {
			return err
		}
		if err := st.SetLevel(ctx, p.Fingerprint, lvl); err != nil {
			return err
		}
		fmt.Printf("%s → THIS-%d (member)\n", p.Handle, lvl)
		return nil

	case "member":
		if len(args) != 3 || (args[2] != "on" && args[2] != "off") {
			return fmt.Errorf("usage: neabbs admin member <handle> on|off")
		}
		p, err := byHandle(args[1])
		if err != nil {
			return err
		}
		if err := st.SetThisMember(ctx, p.Fingerprint, args[2] == "on"); err != nil {
			return err
		}
		fmt.Printf("%s member=%s\n", p.Handle, args[2])
		return nil

	case "ban", "unban":
		if len(args) != 2 {
			return fmt.Errorf("usage: neabbs admin %s <handle>", cmd)
		}
		p, err := byHandle(args[1])
		if err != nil {
			return err
		}
		if err := st.SetBanned(ctx, p.Fingerprint, cmd == "ban"); err != nil {
			return err
		}
		fmt.Printf("%s banned=%v\n", p.Handle, cmd == "ban")
		return nil

	case "sysop":
		if len(args) != 3 || (args[2] != "on" && args[2] != "off") {
			return fmt.Errorf("usage: neabbs admin sysop <handle> on|off")
		}
		p, err := byHandle(args[1])
		if err != nil {
			return err
		}
		if err := st.SetAdmin(ctx, p.Fingerprint, args[2] == "on"); err != nil {
			return err
		}
		fmt.Printf("%s sysop=%s (reconnect to take effect in-game)\n", p.Handle, args[2])
		return nil

	case "flag":
		if len(args) != 3 {
			return fmt.Errorf("usage: neabbs admin flag <handle> <flag>")
		}
		p, err := byHandle(args[1])
		if err != nil {
			return err
		}
		if err := st.GrantFlags(ctx, p.Fingerprint, args[2]); err != nil {
			return err
		}
		fmt.Printf("%s += %s\n", p.Handle, args[2])
		return nil

	default:
		fmt.Fprint(os.Stderr, adminUsage)
		return fmt.Errorf("unknown admin command %q", cmd)
	}
}
