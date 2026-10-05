// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

func requireRoot() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("run the installer with sudo; the bridge drops to its own unprivileged account before accepting requests")
	}
	return nil
}
func dropPrivileges(name string) error {
	u, err := user.Lookup(name)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid == 0 {
		return fmt.Errorf("execution as root is forbidden")
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil || gid == 0 {
		return fmt.Errorf("execution in root group is forbidden")
	}
	if err = syscall.Setgroups([]int{}); err != nil {
		return err
	}
	if err = syscall.Setgid(gid); err != nil {
		return err
	}
	if err = syscall.Setuid(uid); err != nil {
		return err
	}
	// Block same-UID ptrace/proc memory inspection of credentials held by the daemon.
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, 4, 0, 0); errno != 0 {
		return errno
	}
	// Shell children must not gain privilege from setuid executables.
	if _, _, errno := syscall.AllThreadsSyscall6(syscall.SYS_PRCTL, 38, 1, 0, 0, 0, 0); errno != 0 {
		return fmt.Errorf("cannot protect every execution thread; build with CGO_ENABLED=0: %w", errno)
	}
	return nil
}
func prepareProcess(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
