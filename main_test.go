// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"encoding/base64"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testBridge(t *testing.T) *bridge {
	t.Helper()
	password, err := randomPassword()
	if err != nil {
		t.Fatal(err)
	}
	return newBridge(config{Password: password, Port: 23456, PublicHost: "server.example", Origin: "https://ai.example", Home: t.TempDir(), User: "ops"})
}
func callHTTP(b *bridge, body, password string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("POST", "https://server.example:23456/v1/operate", strings.NewReader(body))
	r.Host = "server.example:23456"
	r.Header.Set("Origin", b.cfg.Origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+password)
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	return w
}
func TestGeneratedCredentials(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		p, err := randomPassword()
		if err != nil || len(p) != 43 || seen[p] {
			t.Fatal("password generation failed")
		}
		seen[p] = true
		data, err := base64.RawURLEncoding.DecodeString(p)
		if err != nil || len(data) != 32 {
			t.Fatal("entropy length")
		}
		port, err := randomPort()
		if err != nil || port < 20000 || port > 59999 {
			t.Fatal("port range")
		}
	}
}
func TestAuthenticationIsolationAndStrictJSON(t *testing.T) {
	b := testBridge(t)
	for i := 0; i < 100; i++ {
		if w := callHTTP(b, `{"action":"task_status","args":{}}`, "wrong"); w.Code != 401 {
			t.Fatalf("invalid auth: %d", w.Code)
		}
	}
	if w := callHTTP(b, `{"action":"task_status","args":{}}`, b.cfg.Password); w.Code != 200 {
		t.Fatalf("invalid auth drained valid budget: %d", w.Code)
	}
	for _, body := range []string{`{"action":"task_status","args":{},"password":"x"}`, `{"action":"task_status","args":{}} {}`, `{"action":"task_status","args":{"unknown":1}}`} {
		if w := callHTTP(b, body, b.cfg.Password); w.Code != 400 {
			t.Fatalf("strict parser accepted %s", body)
		}
	}
}
func TestHostOriginPathMethodAndPreflight(t *testing.T) {
	b := testBridge(t)
	for _, test := range []struct {
		host, origin, path, method string
		want                       int
	}{{"evil.example", b.cfg.Origin, "/v1/operate", "POST", 403}, {"server.example:23456", "https://evil.example", "/v1/operate", "POST", 403}, {"server.example:23456", b.cfg.Origin, "/v1/operate?password=x", "POST", 404}, {"server.example:23456", b.cfg.Origin, "/v1/operate", "GET", 405}} {
		r := httptest.NewRequest(test.method, "https://server.example:23456"+test.path, nil)
		r.Host = test.host
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("boundary: got %d want %d", w.Code, test.want)
		}
	}
	r := httptest.NewRequest("OPTIONS", "https://server.example:23456/v1/operate", nil)
	r.Host = "server.example:23456"
	r.Header.Set("Origin", b.cfg.Origin)
	r.Header.Set("Access-Control-Request-Method", "POST")
	r.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != b.cfg.Origin {
		t.Fatal("preflight")
	}
}
func TestNoShutdownEndpointsAndCommandGuard(t *testing.T) {
	b := testBridge(t)
	for _, s := range []string{"shutdown", "reboot", "poweroff", "halt"} {
		if w := callHTTP(b, `{"action":"`+s+`","args":{}}`, b.cfg.Password); w.Code != 400 {
			t.Fatal("forbidden endpoint")
		}
	}
	for _, s := range []string{"reboot", "systemctl --force poweroff", "shutdown -h now", "mkfs.ext4 /dev/x"} {
		if validateCommand(s) == nil {
			t.Fatalf("accepted %s", s)
		}
	}
	if validateCommand("printf 'hello world'") != nil {
		t.Fatal("ordinary command blocked")
	}
}
func TestCronScheduleSemantics(t *testing.T) {
	for _, s := range []string{"60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8", "*/0 * * * *", "* * * *", "@reboot", "* * * * *\n"} {
		if _, err := parseCron(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	s, err := parseCron("*/15 8-10 * * 1-5")
	if err != nil {
		t.Fatal(err)
	}
	if !s.matches(time.Date(2026, 10, 5, 9, 30, 0, 0, time.UTC)) || s.matches(time.Date(2026, 10, 5, 9, 31, 0, 0, time.UTC)) || s.matches(time.Date(2026, 10, 4, 9, 30, 0, 0, time.UTC)) {
		t.Fatal("range/step/weekdays")
	}
	s, _ = parseCron("0 0 1 * 7")
	if !s.matches(time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)) || !s.matches(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("Sunday alias or DOM/DOW OR semantics")
	}
}
func TestBoundedOutput(t *testing.T) {
	o := &boundedOutput{}
	if n, err := o.Write(make([]byte, outputLimit+200)); err != nil || n != outputLimit+200 {
		t.Fatal("writer contract")
	}
	s, truncated := o.snapshot()
	if len(s) != outputLimit || !truncated {
		t.Fatal("unbounded output")
	}
}
func TestNormalizePublicHost(t *testing.T) {
	cases := []struct{ host, origin, want string }{
		{"Bridge.Example.COM.", "https://ai.example", "bridge.example.com"},
		{" 203.0.113.7 ", "https://ai.example", "203.0.113.7"},
		{"[2001:0DB8:0:0::7]", "https://ai.example", "2001:db8::7"},
		{"127.0.0.1", "https://ai.example", "127.0.0.1"},
	}
	for _, tc := range cases {
		if got, err := normalizePublicHost(tc.host, tc.origin); err != nil || got != tc.want {
			t.Fatalf("%q: got %q %v", tc.host, got, err)
		}
	}
	// 跟训练场同主机名（大小写、IPv6 写法不同也算）一律拒绝，格式不对的也拒绝
	for _, tc := range []struct{ host, origin string }{
		{"AI.example", "https://ai.example"},
		{"ai.example", "https://ai.example:8443"},
		{"2001:db8::7", "https://[2001:0db8::7]"},
		{"bad host", "https://ai.example"},
		{"", "https://ai.example"},
	} {
		if _, err := normalizePublicHost(tc.host, tc.origin); err == nil {
			t.Fatalf("%q accepted for %s", tc.host, tc.origin)
		}
	}
}
