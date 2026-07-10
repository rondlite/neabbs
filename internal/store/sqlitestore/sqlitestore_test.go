package sqlitestore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/rondlite/neabbs/internal/store"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPlayerLifecycle(t *testing.T) {
	s := open(t)
	ctx := context.Background()

	if _, err := s.PlayerByFingerprint(ctx, "fp1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	p, err := s.CreatePlayer(ctx, "fp1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Handle != "" || p.ThisMember || p.Level != 0 || p.Banned || p.Speed != 1200 {
		t.Fatalf("bad defaults: %+v", p)
	}

	if err := s.SetHandle(ctx, "fp1", "wodan"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePlayer(ctx, "fp2"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetHandle(ctx, "fp2", "wodan"); !errors.Is(err, store.ErrHandleTaken) {
		t.Fatalf("want ErrHandleTaken, got %v", err)
	}

	if err := s.GrantFlags(ctx, "fp1", "this_invite", "found_x"); err != nil {
		t.Fatal(err)
	}
	if err := s.GrantFlags(ctx, "fp1", "found_x"); err != nil { // idempotent
		t.Fatal(err)
	}
	p, _ = s.PlayerByFingerprint(ctx, "fp1")
	if !p.HasFlag("this_invite") || !p.HasFlag("found_x") || len(p.Flags) != 2 {
		t.Fatalf("flags wrong: %+v", p.Flags)
	}

	if err := s.SetLevel(ctx, "fp1", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.SetLevel(ctx, "fp1", 12); err == nil {
		t.Fatal("level 12 accepted")
	}
	if err := s.SetThisMember(ctx, "fp1", true); err != nil {
		t.Fatal(err)
	}
	p, _ = s.PlayerByFingerprint(ctx, "fp1")
	if p.Level != 3 || !p.ThisMember {
		t.Fatalf("level/member wrong: %+v", p)
	}

	byHandle, err := s.PlayerByHandle(ctx, "wodan")
	if err != nil || byHandle.Fingerprint != "fp1" {
		t.Fatalf("PlayerByHandle: %v %+v", err, byHandle)
	}
}

func TestMinutesRollover(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	s.CreatePlayer(ctx, "fp1")

	used, err := s.AddMinutes(ctx, "fp1", "2026-07-10", 5)
	if err != nil || used != 5 {
		t.Fatalf("got %d, %v", used, err)
	}
	used, _ = s.AddMinutes(ctx, "fp1", "2026-07-10", 3)
	if used != 8 {
		t.Fatalf("same day: got %d, want 8", used)
	}
	used, _ = s.AddMinutes(ctx, "fp1", "2026-07-11", 2)
	if used != 2 {
		t.Fatalf("rollover: got %d, want 2", used)
	}
}

func TestCallersLog(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	base := time.Unix(1000000, 0)
	for i, h := range []string{"a", "b", "c"} {
		s.RecordCall(ctx, h, base.Add(time.Duration(i)*time.Minute))
	}
	got, err := s.LastCallers(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Handle != "c" || got[1].Handle != "b" {
		t.Fatalf("wrong callers: %+v", got)
	}
}

func TestLastRead(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if n, _ := s.LastRead(ctx, "fp1", "algemeen"); n != 0 {
		t.Fatalf("empty high-water = %d", n)
	}
	s.SetLastRead(ctx, "fp1", "algemeen", 42)
	s.SetLastRead(ctx, "fp1", "algemeen", 7) // never lowers
	if n, _ := s.LastRead(ctx, "fp1", "algemeen"); n != 42 {
		t.Fatalf("high-water = %d, want 42", n)
	}
}

func TestPostsIDSpace(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, err := s.SavePost(ctx, &store.SavedMessage{
		BoardID: "algemeen", Author: "wodan", Subject: "hoi", Body: "x", PostedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id < 10000 {
		t.Fatalf("player post id %d collides with YAML id space", id)
	}
	posts, err := s.PostsForBoard(ctx, "algemeen")
	if err != nil || len(posts) != 1 || posts[0].ID != id {
		t.Fatalf("PostsForBoard: %v %+v", err, posts)
	}
}
