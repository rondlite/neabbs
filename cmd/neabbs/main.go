// neabbs is the NEABBS SSH BBS daemon (and, later, its admin CLI).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/ssh"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"

	"github.com/rondlite/neabbs/internal/board"
	"github.com/rondlite/neabbs/internal/chat"
	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/content"
	"github.com/rondlite/neabbs/internal/ghosts"
	"github.com/rondlite/neabbs/internal/llm"
	"github.com/rondlite/neabbs/internal/presence"
	"github.com/rondlite/neabbs/internal/sshd"
	"github.com/rondlite/neabbs/internal/store"
	"github.com/rondlite/neabbs/internal/store/sqlitestore"
	"github.com/rondlite/neabbs/internal/tui"
	"github.com/rondlite/neabbs/internal/web"
	"github.com/rondlite/neabbs/internal/world"
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
		case "genposts":
			return runGenposts(args[1:])
		default:
			return fmt.Errorf("unknown subcommand %q (want serve, admin, or genposts)", args[0])
		}
	}
	cfg := config.FromEnv()

	// If the container started as root (platforms often mount volumes
	// root-owned), fix ownership of the writable dirs and drop to the
	// unprivileged user before touching the DB.
	if err := dropPrivileges(filepath.Dir(cfg.DBPath), filepath.Dir(cfg.HostKey), cfg.CertsDir); err != nil {
		return fmt.Errorf("drop privileges: %w", err)
	}

	// lipgloss's global renderer detects the daemon's stdout (not a TTY, so
	// colour would be stripped). Every session is an interactive PTY over
	// SSH, so force 256-colour output; otherwise all styling renders plain.
	lipgloss.SetColorProfile(termenv.ANSI256)

	st, err := sqlitestore.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open db %q (is the directory writable by this user?): %w", cfg.DBPath, err)
	}
	defer st.Close()

	cset, err := content.Load(cfg.ContentDir)
	if err != nil {
		return fmt.Errorf("load content: %w", err)
	}
	slog.Info("content loaded", "boards", len(cset.Boards))

	registry := presence.NewRegistry()

	// The old crowd trickles back: seeded 1980s callers dial in now and then so
	// the "laatste bellers" list looks like a board in use. Pure fiction — it
	// never touches the DB or the public stats.
	handles := make([]string, 0, len(cset.SeedCallers))
	for _, c := range cset.SeedCallers {
		handles = append(handles, c.Handle)
	}
	roster := ghosts.New(handles, time.Now(), rand.New(rand.NewSource(time.Now().UnixNano())))
	ghostsDone := make(chan struct{})
	go roster.Run(ghostsDone)
	defer close(ghostsDone)

	// Landing site (optional). A web failure logs and never stops the game.
	if cfg.WebListen != "" {
		ws := web.New(cfg, registry, st)
		go func() {
			slog.Info("web listening", "addr", cfg.WebListen)
			if err := ws.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				slog.Error("web server", "err", err)
			}
		}()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = ws.Shutdown(ctx)
		}()
	}

	srv, err := newServer(cfg, st, registry, board.NewEngine(cset, st), cset, chat.NewRoom(), world.NewEngine(cset, st), llm.New(cfg), roster)
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

func newServer(cfg config.Config, st store.Store, registry *presence.Registry, engine *board.Engine, cset *content.Set, room *chat.Room, w *world.Engine, lc *llm.Client, gr *ghosts.Roster) (*ssh.Server, error) {
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
			Content:  cset,
			Chat:     room,
			World:    w,
			LLM:      lc,
			Ghosts:   gr,
		})
		p := tea.NewProgram(m, append(bm.MakeOptions(s), tea.WithoutSignalHandler())...)
		sess.SetSend(func(msg any) { p.Send(msg) })
		return p
	}, termenv.ANSI256)

	return sshd.New(cfg, teaMW, lifecycleMiddleware(st, registry, room))
}

// lifecycleMiddleware enforces session caps, loads/creates the player row
// (the pubkey fingerprint IS the identity), refuses banned players, and
// guarantees registry cleanup when the session ends.
func lifecycleMiddleware(st store.Store, registry *presence.Registry, room *chat.Room) func(next ssh.Handler) ssh.Handler {
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
			// Abrupt disconnects must always free the line and leave the
			// chat room, whatever state the UI was in.
			defer registry.Remove(sess)
			defer room.Leave(sess)

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
