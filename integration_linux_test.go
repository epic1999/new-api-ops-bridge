// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
//go:build linux

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
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

// 跑 init 生成一份全新的状态目录
func initBridgeState(t *testing.T, binary, username string) (string, config) {
	t.Helper()
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
	return state, c
}

// 以 root 启动 serve；返回的函数负责停掉进程并交回日志
func serveBridge(t *testing.T, binary, state string) func() []byte {
	t.Helper()
	logPath := filepath.Join(filepath.Dir(state), "serve.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "serve", "--state", state)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	return func() []byte {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(6 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		logFile.Close()
		data, _ := os.ReadFile(logPath)
		return data
	}
}

// 只信任本机 CA 去连桥，等它起来并拿到实际下发的服务证书
func servedCertificate(t *testing.T, state string, port int) *x509.Certificate {
	t.Helper()
	caPEM, err := os.ReadFile(filepath.Join(state, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("local CA")
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for deadline := time.Now().Add(5 * time.Second); ; {
		conn, err := tls.Dial("tcp", address, &tls.Config{RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12})
		if err == nil {
			defer conn.Close()
			return conn.ConnectionState().PeerCertificates[0]
		}
		if time.Now().After(deadline) {
			t.Fatal("TLS serving startup failed:", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 证书只剩 10 天：serve 启动时要沿用原 CA 续上，renew-cert 也能手动再续
func TestExpiringCertificateRenewsWithSameLocalCA(t *testing.T) {
	binary, username := os.Getenv("BRIDGE_TEST_BINARY"), os.Getenv("BRIDGE_TEST_USER")
	if binary == "" || username == "" || os.Geteuid() != 0 {
		t.Skip("requires disposable root Linux integration environment")
	}
	state, c := initBridgeState(t, binary, username)
	aged := time.Now().Add(10*24*time.Hour - leafLifetime)
	_, writes, err := issueCertificates(c.PublicHost, aged, tlsMaterial{})
	if err != nil {
		t.Fatal(err)
	}
	if err = commitCertificateFiles(state, writes); err != nil {
		t.Fatal(err)
	}
	caBefore, err := os.ReadFile(filepath.Join(state, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	stop := serveBridge(t, binary, state)
	served := servedCertificate(t, state, c.Port)
	logs := stop()
	if time.Until(served.NotAfter) < leafLifetime-time.Hour || !bytes.Contains(logs, []byte("TLS certificate renewed")) {
		t.Fatalf("startup did not renew the expiring certificate: %s", logs)
	}
	if bytes.Contains(logs, []byte(c.Password)) {
		t.Error("password leaked in serving logs")
	}
	// 手动续期：服务证书换新，CA 保持不变，也不能把密码打到终端
	out, err := exec.Command(binary, "renew-cert", "--state", state).CombinedOutput()
	if err != nil || bytes.Contains(out, []byte(c.Password)) || !bytes.Contains(out, []byte("Local CA SHA-256 fingerprint")) {
		t.Fatalf("renew-cert failed: %v %s", err, out)
	}
	files := map[string][]byte{}
	for _, name := range certificateFiles {
		data, err := readPrivateFile(filepath.Join(state, name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = data
	}
	current, err := parseCertificates(files)
	if err != nil || !current.managed || !bytes.Equal(files["ca.pem"], caBefore) || bytes.Equal(current.leaf.Raw, served.Raw) {
		t.Fatalf("renewal did not keep the local CA: %v", err)
	}
}

// Opt in with a separately built CGO_ENABLED=0 binary and a disposable account.
// This exercises actual root startup, TLS trust, UID/GID reduction and children.
func TestRealHTTPSUnprivilegedBridge(t *testing.T) {
	binary, username := os.Getenv("BRIDGE_TEST_BINARY"), os.Getenv("BRIDGE_TEST_USER")
	if binary == "" || username == "" || os.Geteuid() != 0 {
		t.Skip("requires disposable root Linux integration environment")
	}
	state, c := initBridgeState(t, binary, username)
	// 跟用户导入本机 CA 一样，只信任 ca.pem
	caPEM, err := os.ReadFile(filepath.Join(state, "ca.pem"))
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("local CA")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	stop := serveBridge(t, binary, state)
	defer func() {
		if bytes.Contains(stop(), []byte(c.Password)) {
			t.Error("password leaked in serving logs")
		}
	}()
	servedCertificate(t, state, c.Port)
	url := "https://127.0.0.1:" + strconv.Itoa(c.Port)
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
