//go:build !linux

package main

// dropPrivileges is a no-op off Linux (the daemon ships as a Linux
// container; this keeps `go build`/tests green on other dev platforms).
func dropPrivileges(dirs ...string) error { return nil }
