package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rondlite/neabbs/internal/config"
	"github.com/rondlite/neabbs/internal/presence"
)

type fakeStats struct {
	n   int
	err error
}

func (f fakeStats) CountRegistered(context.Context) (int, error) { return f.n, f.err }

func testServer(stats Stats) *Server {
	cfg := config.Config{WebListen: ":8080", WebDomain: "neabbs.com", CertsDir: "./certs"}
	return New(cfg, presence.NewRegistry(), stats)
}

func get(t *testing.T, h http.Handler, path, host string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestIndex(t *testing.T) {
	rec := get(t, testServer(fakeStats{n: 3}).handler(), "/", "neabbs.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "ssh neabbs.com") {
		t.Errorf("index must contain the connect command")
	}
}

func TestStaticAssets(t *testing.T) {
	h := testServer(fakeStats{}).handler()
	for _, p := range []string{"/style.css", "/site.js"} {
		if rec := get(t, h, p, "neabbs.com"); rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", p, rec.Code)
		}
	}
}

func TestIndexHasCRTPage(t *testing.T) {
	body := get(t, testServer(fakeStats{}).handler(), "/", "neabbs.com").Body.String()
	for _, want := range []string{
		"ssh neabbs.com",         // connect command
		"data-nl",                // language toggle machinery
		"VERWIJDERD DOOR SYSOP",  // the THIS glitch row
		"id=\"boot\"",            // boot-sequence hero
	} {
		if !strings.Contains(body, want) {
			t.Errorf("index.html missing %q", want)
		}
	}
}

func TestStatusJSON(t *testing.T) {
	rec := get(t, testServer(fakeStats{n: 42}).handler(), "/api/status", "neabbs.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["registered"] != float64(42) {
		t.Errorf("registered = %v, want 42", got["registered"])
	}
	if got["callers_online"] != float64(0) {
		t.Errorf("callers_online = %v, want 0", got["callers_online"])
	}
	for k := range got {
		switch k {
		case "callers_online", "registered", "reopened":
		default:
			t.Errorf("unexpected field %q in status JSON (no player data may leak)", k)
		}
	}
}

func TestStatusStoreErrorStillServes(t *testing.T) {
	rec := get(t, testServer(fakeStats{err: errors.New("boom")}).handler(), "/api/status", "neabbs.com")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (stale/zero beats broken)", rec.Code)
	}
}

func TestUnknownPath404(t *testing.T) {
	rec := get(t, testServer(fakeStats{}).handler(), "/wp-admin", "neabbs.com")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ONBEKEND COMMANDO") {
		t.Errorf("404 body = %q, want ONBEKEND COMMANDO", rec.Body.String())
	}
}

func TestShutdownBeforeServe(t *testing.T) {
	s := testServer(fakeStats{})
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v, want nil", err)
	}
	if err := s.Serve(); !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve() after Shutdown = %v, want http.ErrServerClosed", err)
	}
}

func TestServeShutdownConcurrent(t *testing.T) {
	cfg := config.Config{WebListen: "127.0.0.1:0", WebDomain: "neabbs.com"}
	s := New(cfg, presence.NewRegistry(), fakeStats{})
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v, want nil", err)
	}
	if err := <-done; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("Serve() = %v, want http.ErrServerClosed", err)
	}
}

func TestWWWRedirectsToApex(t *testing.T) {
	rec := get(t, testServer(fakeStats{}).handler(), "/", "www.neabbs.com")
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "https://neabbs.com/" {
		t.Errorf("Location = %q, want https://neabbs.com/", loc)
	}
}
