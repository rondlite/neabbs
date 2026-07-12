// Package web serves the neabbs.com landing site from the game binary.
// Off unless NEABBS_WEB is set; a web failure never affects the SSH game.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/presence"
	"golang.org/x/crypto/acme/autocert"
)

//go:embed static
var staticFS embed.FS

// reopened is the in-fiction reopening year shown in /api/status.
const reopened = "2026"

// statusTTL caps how often /api/status hits the DB.
const statusTTL = 10 * time.Second

// Stats is the read-only slice of the store the site needs.
type Stats interface {
	CountRegistered(ctx context.Context) (int, error)
}

type status struct {
	CallersOnline int    `json:"callers_online"`
	Registered    int    `json:"registered"`
	Reopened      string `json:"reopened"`
}

// Server is the landing-site HTTP server.
type Server struct {
	cfg      config.Config
	registry *presence.Registry
	stats    Stats

	mu       sync.Mutex
	cached   status
	cachedAt time.Time

	// srvMu guards the listener lifecycle (closed/httpSrv/mainSrv) against
	// concurrent Serve/Shutdown. Distinct from mu, which guards the status cache.
	srvMu   sync.Mutex
	closed  bool
	httpSrv *http.Server // :80 challenge/redirect listener (autocert mode only)
	mainSrv *http.Server
}

// New builds the server; call Serve to start it.
func New(cfg config.Config, registry *presence.Registry, stats Stats) *Server {
	return &Server{cfg: cfg, registry: registry, stats: stats}
}

// Serve blocks until Shutdown or a listener error. ":443" enables
// autocert (Let's Encrypt) with an :80 sidecar for the ACME http-01
// challenge and https redirect; any other address serves plain HTTP (dev).
func (s *Server) Serve() error {
	h := s.handler()
	if s.cfg.WebListen != ":443" {
		srv := &http.Server{
			Addr:              s.cfg.WebListen,
			Handler:           h,
			ReadHeaderTimeout: 5 * time.Second,
		}
		s.srvMu.Lock()
		if s.closed {
			s.srvMu.Unlock()
			return http.ErrServerClosed
		}
		s.mainSrv = srv
		s.srvMu.Unlock()
		return srv.ListenAndServe()
	}

	m := s.certManager()
	httpSrv := &http.Server{
		Addr:              ":80",
		Handler:           m.HTTPHandler(http.HandlerFunc(redirectHTTPS)),
		ReadHeaderTimeout: 5 * time.Second,
	}
	mainSrv := &http.Server{
		Addr:              ":443",
		Handler:           h,
		TLSConfig:         m.TLSConfig(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.srvMu.Lock()
	if s.closed {
		s.srvMu.Unlock()
		return http.ErrServerClosed
	}
	s.httpSrv = httpSrv
	s.mainSrv = mainSrv
	s.srvMu.Unlock()
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("web: :80 listener", "err", err)
		}
	}()
	return mainSrv.ListenAndServeTLS("", "")
}

// Shutdown gracefully stops all listeners.
func (s *Server) Shutdown(ctx context.Context) error {
	s.srvMu.Lock()
	s.closed = true
	servers := []*http.Server{s.httpSrv, s.mainSrv}
	s.srvMu.Unlock()

	var err error
	for _, srv := range servers {
		if srv != nil {
			if e := srv.Shutdown(ctx); e != nil && err == nil {
				err = e
			}
		}
	}
	return err
}

// certManager builds the Let's Encrypt manager; certs cache under CertsDir.
func (s *Server) certManager() *autocert.Manager {
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(s.cfg.CertsDir),
		HostPolicy: autocert.HostWhitelist(s.cfg.WebDomain, "www."+s.cfg.WebDomain),
	}
}

// redirectHTTPS 301s everything that is not an ACME challenge to https.
func redirectHTTPS(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "https://"+hostOnly(r.Host)+r.URL.RequestURI(), http.StatusMovedPermanently)
}

func (s *Server) handler() http.Handler {
	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // embedded FS: impossible unless the build is broken
	}
	files := http.FileServerFS(static)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("ONBEKEND COMMANDO\n"))
			return
		}
		http.ServeFileFS(w, r, static, "index.html")
	})
	mux.Handle("/style.css", files)
	mux.Handle("/site.js", files)
	mux.HandleFunc("/api/status", s.handleStatus)
	return withApexHost(s.cfg.WebDomain, mux)
}

// withApexHost 301s www.<domain> to the apex so autocert only ever has to
// keep both certs but users land on one canonical host.
func withApexHost(domain string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(strings.TrimSuffix(hostOnly(r.Host), "."), "www."+domain) {
			http.Redirect(w, r, "https://"+domain+r.URL.RequestURI(), http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostOnly strips an optional :port from a Host header value.
func hostOnly(h string) string {
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.Contains(h[i:], "]") {
		return h[:i]
	}
	return h
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if time.Since(s.cachedAt) > statusTTL {
		n, err := s.stats.CountRegistered(r.Context())
		if err != nil {
			slog.Error("web: count registered", "err", err)
			n = s.cached.Registered // stale beats broken
		}
		s.cached = status{CallersOnline: s.registry.Count(), Registered: n, Reopened: reopened}
		s.cachedAt = time.Now()
	}
	st := s.cached
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(st)
}
