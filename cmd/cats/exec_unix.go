//go:build !windows

package main

import "syscall"

func execve(path string, argv []string, env []string) error {
	return syscall.Exec(path, argv, env)
}
