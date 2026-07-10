// Package sshd wires up the Wish SSH server. The SSH server is the game:
// no shell, no exec, no filesystem access. Everything but an interactive
// PTY session running the TUI is rejected and logged.
package sshd

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	gossh "golang.org/x/crypto/ssh"

	"github.com/rondlite/neabbs/internal/config"
)

const (
	idleTimeout = 15 * time.Minute
	maxTimeout  = 4 * time.Hour
)

// Fingerprint returns the SHA256 fingerprint of the session's public key —
// the player's identity.
func Fingerprint(key ssh.PublicKey) string {
	return gossh.FingerprintSHA256(key)
}

func remoteInfo(ctx ssh.Context) (fp, addr string) {
	addr = "?"
	if ctx.RemoteAddr() != nil {
		addr = ctx.RemoteAddr().String()
	}
	fp = "(pre-auth)"
	if key, ok := ctx.Value(ssh.ContextKeyPublicKey).(ssh.PublicKey); ok && key != nil {
		fp = Fingerprint(key)
	}
	return fp, addr
}

func logReject(what string, ctx ssh.Context) {
	fp, addr := remoteInfo(ctx)
	slog.Warn("rejected", "what", what, "fingerprint", fp, "remote", addr)
}

func rejectChannel(what string) ssh.ChannelHandler {
	return func(srv *ssh.Server, conn *gossh.ServerConn, newChan gossh.NewChannel, ctx ssh.Context) {
		logReject(what, ctx)
		_ = newChan.Reject(gossh.Prohibited, "not available")
	}
}

// New builds the configured Wish server. teaMW is the Bubble Tea session
// middleware (innermost); lifecycle wraps it (caps, player load, cleanup).
func New(cfg config.Config, teaMW, lifecycle wish.Middleware) (*ssh.Server, error) {
	srv, err := wish.NewServer(
		wish.WithAddress(cfg.Listen),
		wish.WithHostKeyPath(cfg.HostKey),
		// Public-key auth only, accept any key: the fingerprint IS the identity.
		wish.WithPublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
			return true
		}),
		wish.WithIdleTimeout(idleTimeout),
		wish.WithMaxTimeout(maxTimeout),
		// Wish wraps in order: the LAST middleware here runs FIRST.
		wish.WithMiddleware(
			teaMW,
			lifecycle,
			activeterm.Middleware(), // require a PTY
			rejectExecMiddleware(),  // first: log+refuse exec explicitly
		),
	)
	if err != nil {
		return nil, fmt.Errorf("wish server: %w", err)
	}

	// Belt and braces: only the session channel exists; forwarding channels
	// are rejected and logged. Reverse forwarding and subsystems (SFTP/SCP)
	// have no handlers registered, so they are refused too.
	srv.ChannelHandlers = map[string]ssh.ChannelHandler{
		"session":                        ssh.DefaultSessionHandler,
		"direct-tcpip":                   rejectChannel("direct-tcpip"),
		"direct-streamlocal@openssh.com": rejectChannel("direct-streamlocal"),
	}
	srv.RequestHandlers = map[string]ssh.RequestHandler{
		"tcpip-forward": func(ctx ssh.Context, srv *ssh.Server, req *gossh.Request) (bool, []byte) {
			logReject("tcpip-forward", ctx)
			return false, nil
		},
		"cancel-tcpip-forward": func(ctx ssh.Context, srv *ssh.Server, req *gossh.Request) (bool, []byte) {
			return false, nil
		},
	}
	srv.SubsystemHandlers = map[string]ssh.SubsystemHandler{} // none: sftp/scp refused
	srv.LocalPortForwardingCallback = func(ctx ssh.Context, host string, port uint32) bool {
		logReject("local-port-forward", ctx)
		return false
	}
	srv.ReversePortForwardingCallback = func(ctx ssh.Context, host string, port uint32) bool {
		logReject("reverse-port-forward", ctx)
		return false
	}
	return srv, nil
}

// rejectExecMiddleware refuses exec requests (`ssh host somecommand`):
// only interactive sessions are served.
func rejectExecMiddleware() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(s ssh.Session) {
			if len(s.Command()) > 0 {
				logReject("exec", s.Context())
				fmt.Fprintln(s, "NEABBS: geen shell, geen exec. Bel gewoon in: ssh neabbs.com")
				_ = s.Exit(1)
				return
			}
			next(s)
		}
	}
}
