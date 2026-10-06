// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
//go:build linux

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsureCertificateOwnershipAndCustomCertificates(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root setup test")
	}
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	host := "127.0.0.1"
	now := time.Now()
	first, err := ensureCertificate(state, host, now, false)
	if err != nil || !first.renewed || !first.newCA {
		t.Fatalf("first issue: %v", err)
	}
	for _, name := range certificateFiles {
		info, err := os.Lstat(filepath.Join(state, name))
		if err != nil || info.Mode().Perm() != 0600 || !rootOwned(info) {
			t.Fatalf("%s permissions", name)
		}
	}
	// 没到续期窗口就原样用
	again, err := ensureCertificate(state, host, now, false)
	if err != nil || again.renewed || !bytes.Equal(again.leaf.Raw, first.leaf.Raw) {
		t.Fatalf("unexpected renewal: %v", err)
	}
	// CA 私钥被换了属主，说明有人动过，拒绝使用
	caKey := filepath.Join(state, "ca-key.pem")
	if err = os.Chown(caKey, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if _, err = ensureCertificate(state, host, now, false); err == nil {
		t.Fatal("foreign-owned CA key accepted")
	}
	if err = os.Chown(caKey, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 换成用户自己的证书：强制续期报错，但旧证书照样交回去让服务继续跑
	custom := selfSignedFiles(t, "bridge.example", host, now.Add(5*24*time.Hour))
	if err = commitCertificateFiles(state, []pemFile{{"key.pem", custom["key.pem"]}, {"cert.pem", custom["cert.pem"]}}); err != nil {
		t.Fatal(err)
	}
	current, err := ensureCertificate(state, host, now, true)
	if err == nil || current.managed || len(current.pair.Certificate) == 0 {
		t.Fatalf("custom certificate handling: %v", err)
	}
	if !bytes.Equal(current.leaf.Raw, current.pair.Certificate[0]) {
		t.Fatal("custom certificate replaced")
	}
}
