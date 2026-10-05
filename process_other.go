// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
//go:build !linux

package main

import (
	"fmt"
	"os/exec"
)

func requireRoot() error {
	return fmt.Errorf("the server bridge runs on Linux; use the existing new-api Agent Bridge for local desktop tools")
}
func dropPrivileges(string) error  { return requireRoot() }
func prepareProcess(cmd *exec.Cmd) {}
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
