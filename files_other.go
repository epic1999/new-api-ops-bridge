// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
//go:build !linux

package main

import (
	"fmt"
	"os"
)

func rootOwned(os.FileInfo) bool                           { return false }
func validateStateAncestors(string) error                  { return fmt.Errorf("Linux required") }
func validateState(string) error                           { return fmt.Errorf("Linux required") }
func secureOpen(string, bool) (*os.File, error)            { return nil, fmt.Errorf("Linux required") }
func createInDirectory(*os.File, string) (*os.File, error) { return nil, fmt.Errorf("Linux required") }
func removeInDirectory(*os.File, string)                   {}
