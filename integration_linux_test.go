// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
//go:build linux

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"new-api-ops-bridge/common"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Opt in with a separately built CGO_ENABLED=0 binary and a disposable account.
// This exercises actual root startup, TLS trust, UID/GID reduction and children.
func TestRealHTTPSUnprivilegedBridge(t *testing.T) {
	binary, username := os.Getenv("BRIDGE_TEST_BINARY"), os.Getenv("BRIDGE_TEST_USER")
	if binary == "" || username == "" || os.Geteuid() != 0 {
		t.Skip("requires disposable root Linux integration environment")
	}
	state := filepath.Join(t.TempDir(), "state")
	init := exec.Command(binary, "init", "--state", state, "--origin", "https://ai.example", "--public-host", "127.0.0.1", "--user", username)
	init.Stdout = io.Discard
	init.Stderr = io.Discard
	if err := init.Run(); err != nil {
		t.Fatal("initialization:", err)
	}
	c, err := readConfig(state)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := os.ReadFile(filepath.Join(state, "cert.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		t.Fatal("certificate")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	logFile, err := os.Create(filepath.Join(filepath.Dir(state), "serve.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(binary, "serve", "--state", state)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		data, _ := os.ReadFile(logFile.Name())
		if bytes.Contains(data, []byte(c.Password)) {
			t.Error("password leaked in serving logs")
		}
	}()
	url := "https://127.0.0.1:" + strconv.Itoa(c.Port)
	for deadline := time.Now().Add(5 * time.Second); ; {
		res, e := client.Get(url + "/")
		if e == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("TLS serving startup failed")
		}
		time.Sleep(20 * time.Millisecond)
	}
	post := func(action string, a args, password string) (map[string]any, int, error) {
		data, e := common.Marshal(request{Action: action, Args: a})
		if e != nil {
			return nil, 0, e
		}
		r, e := http.NewRequest("POST", url+"/v1/operate", bytes.NewReader(data))
		if e != nil {
			return nil, 0, e
		}
		r.Header.Set("Origin", c.Origin)
		r.Header.Set("Authorization", "Bearer "+password)
		r.Header.Set("Content-Type", "application/json")
		res, e := client.Do(r)
		if e != nil {
			return nil, 0, e
		}
		defer res.Body.Close()
		var body map[string]any
		e = common.DecodeJson(res.Body, &body)
		return body, res.StatusCode, e
	}
	for i := 0; i < 60; i++ {
		_, status, e := post("task_status", args{}, "incorrect")
		if e != nil || status != 401 {
			t.Fatal("invalid auth")
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body, status, e := post("execute", args{Command: "id -u; id -G; for i in 1 2 3 4 5 6 7 8; do sh -c 'grep NoNewPrivs /proc/self/status' & done; wait"}, c.Password)
			if e != nil || status != 200 {
				t.Errorf("execution: %d %v", status, e)
				return
			}
			text, _ := body["output"].(string)
			lines := strings.Split(strings.TrimSpace(text), "\n")
			if len(lines) != 10 || lines[0] == "0" || strings.Contains(" "+lines[1]+" ", " 0 ") {
				t.Errorf("identity/group reduction failed: %q", text)
			}
			for _, line := range lines[2:] {
				if strings.TrimSpace(strings.TrimPrefix(line, "NoNewPrivs:")) != "1" {
					t.Errorf("child thread lacked no-new-privileges: %q", line)
				}
			}
		}()
	}
	wg.Wait()
	if _, status, e := post("read", args{Path: filepath.Join(state, "config.json")}, c.Password); e != nil || status != 400 {
		t.Fatal("state visible to file tools")
	}
}
