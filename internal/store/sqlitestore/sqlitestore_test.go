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
	if p.Handle != "" || p.ThisMember || p.Level != 0 || p.Banned || p.Speed != 2400 {
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

func TestDecayedHeat(t *testing.T) {
	base := time.Unix(1_000_000, 0)
	if got := store.DecayedHeat(0, base, base.Add(time.Hour)); got != 0 {
		t.Fatalf("zero stays zero, got %d", got)
	}
	if got := store.DecayedHeat(30, base, base.Add(10*time.Minute)); got != 30-10*store.HeatDecayPerMin {
		t.Fatalf("10-min decay wrong: %d", got)
	}
	if got := store.DecayedHeat(5, base, base.Add(time.Hour)); got != 0 {
		t.Fatalf("heat floors at zero, got %d", got)
	}
	if got := store.DecayedHeat(30, time.Time{}, base); got != 30 {
		t.Fatalf("no basis = no decay, got %d", got)
	}
}

func TestAddHeat(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.CreatePlayer(ctx, "fp1"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.AddHeat(ctx, "fp1", store.HeatCaught); err != nil || v != store.HeatCaught {
		t.Fatalf("first add: v=%d err=%v", v, err)
	}
	// Clamps to HeatMax.
	if v, _ := s.AddHeat(ctx, "fp1", 999); v != store.HeatMax {
		t.Fatalf("clamp high: %d", v)
	}
	// Never below zero.
	if v, _ := s.AddHeat(ctx, "fp1", -999); v != 0 {
		t.Fatalf("clamp low: %d", v)
	}
	// Persisted value is visible on reload.
	if _, err := s.AddHeat(ctx, "fp1", 20); err != nil {
		t.Fatal(err)
	}
	p, _ := s.PlayerByFingerprint(ctx, "fp1")
	if got := p.CurrentHeat(time.Now()); got != 20 {
		t.Fatalf("reload heat = %d, want 20", got)
	}
}

func TestBreaches(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if bi, err := s.BreachInfo(ctx, "h1"); err != nil || bi.Count != 0 {
		t.Fatalf("empty host: %+v err=%v", bi, err)
	}
	// alice first (earlier timestamp), then bob; alice recorded twice = idempotent.
	if err := s.RecordBreach(ctx, "h1", "alice", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	_ = s.RecordBreach(ctx, "h1", "alice", time.Unix(999, 0)) // ignored, keeps first
	_ = s.RecordBreach(ctx, "h1", "bob", time.Unix(200, 0))
	bi, err := s.BreachInfo(ctx, "h1")
	if err != nil || bi.Count != 2 || bi.FirstHandle != "alice" {
		t.Fatalf("breach info: %+v err=%v", bi, err)
	}
	if bi.FirstAt.Unix() != 100 {
		t.Fatalf("first-at not preserved: %d", bi.FirstAt.Unix())
	}
}

func TestLeaderboard(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	mk := func(fp, handle string, lvl int, hosts ...string) {
		if _, err := s.CreatePlayer(ctx, fp); err != nil {
			t.Fatal(err)
		}
		_ = s.SetHandle(ctx, fp, handle)
		_ = s.SetThisMember(ctx, fp, true)
		_ = s.SetLevel(ctx, fp, lvl)
		for i, h := range hosts {
			_ = s.RecordBreach(ctx, h, handle, time.Unix(int64(i+1), 0))
		}
	}
	mk("fp1", "novice", 1)
	mk("fp2", "ace", 9, "a", "b", "c")
	mk("fp3", "middler", 5, "a")
	top, err := s.Leaderboard(ctx, 10)
	if err != nil || len(top) != 3 {
		t.Fatalf("leaderboard: %v %+v", err, top)
	}
	if top[0].Handle != "ace" || top[0].Level != 9 || top[0].Breaches != 3 {
		t.Fatalf("rank 1 wrong: %+v", top[0])
	}
	if top[1].Handle != "middler" || top[2].Handle != "novice" {
		t.Fatalf("ordering wrong: %+v", top)
	}
}

func TestPendingQueue(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	// A draft is hidden from the board until published.
	draftID, err := s.SavePost(ctx, &store.SavedMessage{
		BoardID: "algemeen", Author: "npc", Subject: "concept", Body: "x",
		Pending: true, PostedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if posts, _ := s.PostsForBoard(ctx, "algemeen"); len(posts) != 0 {
		t.Fatalf("draft must not be visible on the board: %+v", posts)
	}
	pend, err := s.PendingPosts(ctx)
	if err != nil || len(pend) != 1 || pend[0].ID != draftID {
		t.Fatalf("PendingPosts: %v %+v", err, pend)
	}
	// nee on a non-existent id is a no-op.
	if ok, _ := s.DeletePendingPost(ctx, 999999); ok {
		t.Fatal("DeletePendingPost of missing id must be false")
	}
	// ok publishes: leaves the queue, appears on the board.
	if ok, err := s.PublishPost(ctx, draftID); err != nil || !ok {
		t.Fatalf("PublishPost: ok=%v err=%v", ok, err)
	}
	if pend, _ := s.PendingPosts(ctx); len(pend) != 0 {
		t.Fatalf("published post must leave the queue: %+v", pend)
	}
	if posts, _ := s.PostsForBoard(ctx, "algemeen"); len(posts) != 1 || posts[0].ID != draftID {
		t.Fatalf("published post must show on the board: %+v", posts)
	}
	// Publishing again is a no-op (already live, not pending).
	if ok, _ := s.PublishPost(ctx, draftID); ok {
		t.Fatal("re-publish of a live post must be false")
	}
	// DeletePendingPost must never touch a live post.
	if ok, _ := s.DeletePendingPost(ctx, draftID); ok {
		t.Fatal("DeletePendingPost must not delete a live post")
	}
}

func TestAdminBit(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	if _, err := s.CreatePlayer(ctx, "fp1"); err != nil {
		t.Fatal(err)
	}
	p, _ := s.PlayerByFingerprint(ctx, "fp1")
	if p.Admin {
		t.Fatal("new player must not be sysop")
	}
	if err := s.SetAdmin(ctx, "fp1", true); err != nil {
		t.Fatal(err)
	}
	p, _ = s.PlayerByFingerprint(ctx, "fp1")
	if !p.Admin {
		t.Fatal("SetAdmin(true) did not stick")
	}
	if err := s.SetAdmin(ctx, "fp1", false); err != nil {
		t.Fatal(err)
	}
	p, _ = s.PlayerByFingerprint(ctx, "fp1")
	if p.Admin {
		t.Fatal("SetAdmin(false) did not stick")
	}
}

func TestDeletePost(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	id, err := s.SavePost(ctx, &store.SavedMessage{
		BoardID: "algemeen", Author: "wodan", Subject: "weg", Body: "x", PostedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Immutable content id (< 10000) is never in the posts table.
	if ok, err := s.DeletePost(ctx, "algemeen", 5); err != nil || ok {
		t.Fatalf("delete of content id should be no-op: ok=%v err=%v", ok, err)
	}
	// A real player post deletes once, then reports gone.
	if ok, err := s.DeletePost(ctx, "algemeen", id); err != nil || !ok {
		t.Fatalf("first delete should succeed: ok=%v err=%v", ok, err)
	}
	if ok, err := s.DeletePost(ctx, "algemeen", id); err != nil || ok {
		t.Fatalf("second delete should be no-op: ok=%v err=%v", ok, err)
	}
	posts, err := s.PostsForBoard(ctx, "algemeen")
	if err != nil || len(posts) != 0 {
		t.Fatalf("board should be empty after delete: %v %+v", err, posts)
	}
}
