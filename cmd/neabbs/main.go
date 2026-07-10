// neabbs is the NEABBS SSH BBS daemon (and, later, its admin CLI).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/ssh"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/presence"
	"github.com/rondlite/neabbs/internal/sshd"
	"github.com/rondlite/neabbs/internal/store"
	"github.com/rondlite/neabbs/internal/store/sqlitestore"
	"github.com/rondlite/neabbs/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "neabbs:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "serve":
			// fall through to the daemon
		case "admin":
			return runAdmin(args[1:])
		default:
			return fmt.Errorf("unknown subcommand %q (want serve or admin)", args[0])
		}
	}
	cfg := config.FromEnv()

	st, err := sqlitestore.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer st.Close()

	cset, err := content.Load(cfg.ContentDir)
	if err != nil {
		return fmt.Errorf("load content: %w", err)
	}
	slog.Info("content loaded", "boards", len(cset.Boards))

	registry := presence.NewRegistry()
	srv, err := newServer(cfg, st, registry, board.NewEngine(cset, st))
	if err != nil {
		return err
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGTERM)
	go func() {
		slog.Info("listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			slog.Error("server", "err", err)
			done <- syscall.SIGTERM
		}
	}()
	<-done
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

// ctx keys for values the lifecycle middleware hands to the tea handler.
type ctxKey string

const (
	ctxSession ctxKey = "neabbs-session"
	ctxPlayer  ctxKey = "neabbs-player"
)

func newServer(cfg config.Config, st store.Store, registry *presence.Registry, engine *board.Engine) (*ssh.Server, error) {
	teaMW := bm.MiddlewareWithProgramHandler(func(s ssh.Session) *tea.Program {
		sess, _ := s.Context().Value(ctxSession).(*presence.Session)
		player, _ := s.Context().Value(ctxPlayer).(*store.Player)
		if sess == nil || player == nil {
			return nil // lifecycle middleware refused the session
		}
		m := tui.New(tui.Deps{
			Cfg:      cfg,
			Store:    st,
			Registry: registry,
			Sess:     sess,
			Player:   player,
			Boards:   engine,
		})
		p := tea.NewProgram(m, append(bm.MakeOptions(s), tea.WithoutSignalHandler())...)
		sess.SetSend(func(msg any) { p.Send(msg) })
		return p
	}, termenv.ANSI256)

	return sshd.New(cfg, teaMW, lifecycleMiddleware(st, registry))
}

// lifecycleMiddleware enforces session caps, loads/creates the player row
// (the pubkey fingerprint IS the identity), refuses banned players, and
// guarantees registry cleanup when the session ends.
func lifecycleMiddleware(st store.Store, registry *presence.Registry) func(next ssh.Handler) ssh.Handler {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			fp := sshd.Fingerprint(s.PublicKey())
			ctx := s.Context()

			sess, err := registry.Add(fp)
			if err != nil {
				slog.Warn("session refused", "reason", err, "fingerprint", fp, "remote", s.RemoteAddr())
				switch {
				case errors.Is(err, presence.ErrTooManySessions):
					fmt.Fprintln(s, "Te veel gelijktijdige verbindingen met deze sleutel.")
				default:
					fmt.Fprintln(s, "ALLE LIJNEN BEZET — probeer het later opnieuw.")
				}
				_ = s.Exit(1)
				return
			}
			defer registry.Remove(sess)

			player, err := st.PlayerByFingerprint(ctx, fp)
			if errors.Is(err, store.ErrNotFound) {
				player, err = st.CreatePlayer(ctx, fp)
			}
			if err != nil {
				slog.Error("load player", "err", err, "fingerprint", fp)
				fmt.Fprintln(s, "Systeemfout. Probeer het later opnieuw.")
				_ = s.Exit(1)
				return
			}
			if player.Banned {
				slog.Warn("banned player refused", "fingerprint", fp, "remote", s.RemoteAddr())
				fmt.Fprintln(s, "TOEGANG GEWEIGERD.")
				_ = s.Exit(1)
				return
			}
			sess.SetHandle(player.Handle)
			_ = st.TouchLastSeen(ctx, fp)

			ctx.SetValue(ctxSession, sess)
			ctx.SetValue(ctxPlayer, player)
			next(s)
			_ = st.TouchLastSeen(context.Background(), fp)
		}
	}
}
