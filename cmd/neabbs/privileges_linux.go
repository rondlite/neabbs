//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// dropPrivileges self-heals the data volume when the container starts as
// root — common on platforms (Fly, Railway, Coolify, k8s, …) that mount
// volumes root-owned. It chowns the directories holding the DB and hostkey
// to the target uid/gid, then permanently drops to that unprivileged user
// before anything else runs.
//
// No-op when already unprivileged (e.g. the image is run with --user 65532
// against an already-writable volume): the process simply serves as-is.
// The target uid/gid default to the distroless nonroot user (65532) and are
// overridable with NEABBS_UID / NEABBS_GID.
func dropPrivileges(dirs ...string) error {
	if syscall.Geteuid() != 0 {
		return nil
	}
	uid := envInt("NEABBS_UID", 65532)
	gid := envInt("NEABBS_GID", 65532)

	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" || dir == "." || seen[dir] {
			continue
		}
		seen[dir] = true
		chownTree(dir, uid, gid)
	}

	if err := syscall.Setgroups([]int{gid}); err != nil {
		return err
	}
	// gid before uid: once uid drops, we can no longer change gid.
	if err := syscall.Setgid(gid); err != nil {
		return err
	}
	if err := syscall.Setuid(uid); err != nil {
		return err
	}
	return nil
}

// chownTree best-effort chowns root and everything under it.
func chownTree(root string, uid, gid int) {
	_ = filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err == nil {
			_ = os.Chown(p, uid, gid)
		}
		return nil // never abort: partial ownership is better than none
	})
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
