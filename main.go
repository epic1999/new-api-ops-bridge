// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"new-api-ops-bridge/common"
)

var version = "dev"

const stateDefault = "/etc/new-api-ops-bridge"

type config struct {
	Password   string `json:"password"`
	Port       int    `json:"port"`
	Origin     string `json:"origin"`
	PublicHost string `json:"public_host"`
	User       string `json:"user"`
	Home       string `json:"home"`
}
type request struct {
	Action string `json:"action"`
	Args   args   `json:"args"`
}
type args struct {
	Command        string `json:"command,omitempty"`
	Cwd            string `json:"cwd,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Path           string `json:"path,omitempty"`
	Offset         int64  `json:"offset,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Data           string `json:"data,omitempty"`
	Schedule       string `json:"schedule,omitempty"`
	ID             string `json:"id,omitempty"`
	Host           string `json:"host,omitempty"`
	Port           int    `json:"port,omitempty"`
}
type bridge struct {
	cfg      config
	secret   [32]byte
	slots    chan struct{}
	mu       sync.Mutex
	tokens   float64
	last     time.Time
	tasks    map[string]*task
	cronMu   sync.Mutex
	state    string
	cronJobs []cronJob
}

func randomPassword() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func randomPort() (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(40000))
	if err != nil {
		return 0, err
	}
	return 20000 + int(n.Int64()), nil
}
func validOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("enter the exact playground origin, e.g. https://ai.example.com")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return "", fmt.Errorf("playground origin must use HTTPS (except localhost development)")
	}
	return u.Scheme + "://" + u.Host, nil
}
func newBridge(c config) *bridge {
	return &bridge{cfg: c, secret: sha256.Sum256([]byte(c.Password)), slots: make(chan struct{}, 4), tokens: 30, last: time.Now(), tasks: make(map[string]*task)}
}
func (b *bridge) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * 5
	if b.tokens > 30 {
		b.tokens = 30
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
func reply(w http.ResponseWriter, status int, body any) {
	data, err := common.Marshal(body)
	if err != nil {
		http.Error(w, "response encoding failed", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
func (b *bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	// Host validation also blocks DNS rebinding to the bridge's listener.
	if r.Host != net.JoinHostPort(b.cfg.PublicHost, fmt.Sprint(b.cfg.Port)) {
		reply(w, 403, map[string]string{"error": "host denied"})
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("new-api Ops Bridge " + version + "\nHTTPS connection established. Copy the generated URL and password from your server terminal to the playground. Never put the password in chat.\n"))
		return
	}
	// Neither a cookie nor query-string password can authorize a request.
	if r.Header.Get("Origin") != b.cfg.Origin {
		reply(w, 403, map[string]string{"error": "origin denied"})
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", b.cfg.Origin)
	w.Header().Set("Vary", "Origin")
	if r.URL.Path != "/v1/operate" || r.URL.RawQuery != "" {
		reply(w, 404, map[string]string{"error": "not found"})
		return
	}
	if r.Method == "OPTIONS" {
		if r.Header.Get("Access-Control-Request-Method") != "POST" {
			reply(w, 403, map[string]string{"error": "method denied"})
			return
		}
		for _, header := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
			header = strings.ToLower(strings.TrimSpace(header))
			if header != "authorization" && header != "content-type" && header != "" {
				reply(w, 403, map[string]string{"error": "header denied"})
				return
			}
		}
		w.Header().Set("Access-Control-Allow-Methods", "POST")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "300")
		w.WriteHeader(204)
		return
	}
	if r.Method != "POST" {
		reply(w, 405, map[string]string{"error": "POST required"})
		return
	}
	supplied := sha256.Sum256([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare(supplied[:], b.secret[:]) != 1 {
		reply(w, 401, map[string]string{"error": "invalid bridge password"})
		return
	}
	// Invalid passwords cannot consume the authenticated caller's quota.
	if !b.allow() {
		reply(w, 429, map[string]string{"error": "too many requests"})
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		reply(w, 415, map[string]string{"error": "application/json required"})
		return
	}
	select {
	case b.slots <- struct{}{}:
		defer func() { <-b.slots }()
	default:
		reply(w, 429, map[string]string{"error": "bridge busy (max 4 requests)"})
		return
	}
	var req request
	if err := common.DecodeJson(http.MaxBytesReader(w, r.Body, 23*1024*1024), &req); err != nil {
		reply(w, 400, map[string]string{"error": "invalid or oversized request"})
		return
	}
	result, err := b.operate(r.Context(), req.Action, req.Args)
	if err != nil {
		reply(w, 400, map[string]string{"error": b.redact(err.Error())})
		return
	}
	// Defense in depth: redact the credential even if a command or file echoes it.
	data, err := common.Marshal(result)
	if err != nil {
		reply(w, 500, map[string]string{"error": "encoding failed"})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write([]byte(b.redact(string(data))))
}
func (b *bridge) redact(s string) string { return strings.ReplaceAll(s, b.cfg.Password, "[REDACTED]") }
func readConfig(state string) (config, error) {
	var c config
	if err := validateState(state); err != nil {
		return c, err
	}
	info, err := os.Lstat(filepath.Join(state, "config.json"))
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !rootOwned(info) {
		return c, fmt.Errorf("config must be a regular owner-only file (chmod 600)")
	}
	data, err := os.ReadFile(filepath.Join(state, "config.json"))
	if err != nil {
		return c, err
	}
	if err = common.DecodeBytes(data, &c); err != nil {
		return c, err
	}
	if len(c.Password) != 43 || c.Port < 20000 || c.Port > 59999 {
		return c, fmt.Errorf("invalid generated credentials; run init")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(c.Password)
	if err != nil || len(decoded) != 32 {
		return c, fmt.Errorf("invalid generated password")
	}
	origin, err := validOrigin(c.Origin)
	if err != nil || origin != c.Origin {
		return c, fmt.Errorf("invalid allowed origin")
	}
	if !validHost(c.PublicHost) || !filepath.IsAbs(c.Home) {
		return c, fmt.Errorf("invalid host or home")
	}
	return c, nil
}
func writeConfig(state string, c config) error {
	if err := validateState(state); err != nil {
		return err
	}
	data, err := common.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(state, ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(state, "config.json"))
}
func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: new-api-ops-bridge init|serve|credentials|rotate|renew-cert|version [options]")
	}
	if os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	state := fs.String("state", stateDefault, "root-owned credential directory")
	origin := fs.String("origin", "", "exact browser origin allowed to connect")
	host := fs.String("public-host", "", "public IP or TLS hostname")
	username := fs.String("user", "new-api-ops", "dedicated non-root execution user")
	fs.Parse(os.Args[2:])
	if fs.NArg() != 0 {
		log.Fatal("unexpected arguments; custom passwords and ports are forbidden")
	}
	var err error
	switch os.Args[1] {
	case "init":
		err = initialize(*state, *origin, *host, *username)
	case "credentials":
		if err = requireRoot(); err == nil {
			var c config
			c, err = readConfig(*state)
			if err == nil {
				printCredentials(c, *state)
			}
		}
	case "rotate":
		if err = requireRoot(); err == nil {
			var c config
			c, err = readConfig(*state)
			if err == nil {
				c.Password, err = randomPassword()
				if err == nil {
					err = writeConfig(*state, c)
					if err == nil {
						printCredentials(c, *state)
						fmt.Println("Restart the bridge service to apply the new password.")
					}
				}
			}
		}
	case "renew-cert":
		// 手动立即续证，原 CA 能用就接着用；不打印密码，免得留在终端记录里
		if err = requireRoot(); err == nil {
			var c config
			if c, err = readConfig(*state); err == nil {
				if _, err = ensureCertificate(*state, c.PublicHost, time.Now(), true); err == nil {
					printCertificateInfo(*state)
					fmt.Println("Restart the bridge service to use the new certificate. Browsers that trust the local CA need no further action; otherwise trust the new certificate again.")
				}
			}
		}
	case "serve":
		err = serve(*state)
	default:
		err = fmt.Errorf("unknown command")
	}
	// 非零退出码让 systemd 的 Restart=on-failure 重新拉起，启动时完成续证
	if errors.Is(err, errCertificateRenewal) {
		log.Print(err)
		os.Exit(renewExitCode)
	}
	if err != nil {
		log.Fatal(err)
	}
}
func serve(state string) error {
	if err := requireRoot(); err != nil {
		return err
	}
	c, err := readConfig(state)
	if err != nil {
		return err
	}
	// 降权前还是 root，趁这时把快到期的证书续掉；续不了就先用旧证书顶着
	material, renewErr := ensureCertificate(state, c.PublicHost, time.Now(), false)
	if len(material.pair.Certificate) == 0 {
		if renewErr == nil {
			renewErr = fmt.Errorf("no usable TLS certificate")
		}
		return renewErr
	}
	if renewErr != nil {
		fmt.Printf("TLS certificate renewal failed: %v. Keeping the current certificate.\n", renewErr)
	}
	if material.renewed {
		fmt.Printf("TLS certificate renewed; valid until %s; SHA-256 fingerprint %s.\n", formatTime(material.leaf.NotAfter), fingerprint(material.leaf.Raw))
		if material.newCA {
			fmt.Printf("A new local CA was created (SHA-256 %s). Import %s again, or trust the new certificate in the browser.\n", fingerprint(material.ca.Raw), filepath.Join(state, "ca.pem"))
		}
	}
	autoRenew := material.managed && renewErr == nil
	if !autoRenew && time.Until(material.leaf.NotAfter) <= renewBefore {
		warnCertificateExpiry(material.leaf.NotAfter)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("", fmt.Sprint(c.Port)))
	if err != nil {
		return fmt.Errorf("random port unavailable; stop the conflicting listener or reinstall: %w", err)
	}
	defer listener.Close()
	if err = dropPrivileges(c.User); err != nil {
		return err
	}
	if err = os.Chdir(c.Home); err != nil {
		return err
	}
	b := newBridge(c)
	b.state = filepath.Clean(state)
	srv := &http.Server{Handler: b, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 40 * time.Second, WriteTimeout: 135 * time.Second, IdleTimeout: 20 * time.Second, MaxHeaderBytes: 8192, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{material.pair}}, ErrorLog: log.New(os.Stderr, "bridge: ", 0)}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// 看门狗要续证时也走这个 ctx，跟收到退出信号一样优雅收尾
	ctx, cancelRun := context.WithCancel(signalCtx)
	defer cancelRun()
	if err = b.loadCron(); err != nil {
		return err
	}
	go b.runCron(ctx)
	var renewDue atomic.Bool
	go b.watchCertificate(ctx, material.leaf.NotAfter, autoRenew, func() {
		renewDue.Store(true)
		cancelRun()
	})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		<-ctx.Done()
		b.cancelTasks()
		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(timeout)
	}()
	fmt.Printf("new-api Ops Bridge %s listening on HTTPS port %d as %s; passwords are never logged.\n", version, c.Port, c.User)
	err = srv.Serve(tls.NewListener(listener, srv.TLSConfig))
	if err != http.ErrServerClosed {
		return err
	}
	// Serve 一收到 Shutdown 就返回了，等在途请求收尾再退出
	<-stopped
	if renewDue.Load() {
		return errCertificateRenewal
	}
	return nil
}
