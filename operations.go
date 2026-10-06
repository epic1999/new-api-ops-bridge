// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const outputLimit = 64 * 1024
const fileLimit = 16 * 1024 * 1024

var forbiddenCommand = regexp.MustCompile(`(?i)\b(?:shutdown|reboot|poweroff|halt|init\s+[06]|telinit\s+[06]|systemctl\s+(?:--\S+\s+)*(?:reboot|poweroff|halt)|mkfs(?:\.\w+)?|wipefs)\b`)

func validateCommand(command string) error {
	if strings.TrimSpace(command) == "" || len(command) > 16000 || strings.ContainsRune(command, 0) {
		return fmt.Errorf("command must contain 1–16000 characters")
	}
	if forbiddenCommand.MatchString(command) {
		return fmt.Errorf("shutdown, reboot and disk-formatting commands are disabled")
	}
	return nil
}

type boundedOutput struct {
	mu        sync.Mutex
	data      []byte
	truncated bool
}

func (o *boundedOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	room := outputLimit - len(o.data)
	if len(p) > room {
		p = p[:room]
		o.truncated = true
	}
	o.data = append(o.data, p...)
	return n, nil
}
func (o *boundedOutput) snapshot() (string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return string(o.data), o.truncated
}

type task struct {
	mu      sync.Mutex
	ID      string
	Started time.Time
	Done    bool
	Error   string
	Output  *boundedOutput
	Cancel  context.CancelFunc
}

func clamp(n, def, max int) int {
	if n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
func (b *bridge) command(ctx context.Context, a args, timeout int, output *boundedOutput) error {
	if err := validateCommand(a.Command); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", a.Command)
	cmd.Dir = b.cfg.Home
	if a.Cwd != "" {
		dir, err := b.path(a.Cwd)
		if err != nil {
			return err
		}
		cmd.Dir = dir
	}
	// Never inherit the daemon environment, auth headers, certificate paths or passwords.
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + b.cfg.Home, "LANG=C.UTF-8", "USER=" + b.cfg.User}
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.Stdin = nil
	prepareProcess(cmd)
	cmd.Cancel = func() error { killProcess(cmd); return nil }
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	defer killProcess(cmd)
	err := cmd.Wait()
	if ctx.Err() != nil {
		return fmt.Errorf("command cancelled or timed out")
	}
	return err
}
func (b *bridge) execute(ctx context.Context, a args) any {
	output := &boundedOutput{}
	err := b.command(ctx, a, clamp(a.TimeoutSeconds, 30, 120), output)
	text, truncated := output.snapshot()
	result := map[string]any{"output": text, "truncated": truncated, "ok": err == nil}
	if err != nil {
		result["error"] = err.Error()
	}
	return result
}
func (b *bridge) startTask(a args) (any, error) {
	if err := validateCommand(a.Command); err != nil {
		return nil, err
	}
	if a.Cwd != "" {
		if _, err := b.path(a.Cwd); err != nil {
			return nil, err
		}
	}
	id, err := randomPassword()
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	active := 0
	for k, t := range b.tasks {
		t.mu.Lock()
		if !t.Done {
			active++
		} else if time.Since(t.Started) > 24*time.Hour || len(b.tasks) >= 64 {
			delete(b.tasks, k)
		}
		t.mu.Unlock()
	}
	if active >= 4 || len(b.tasks) >= 64 {
		b.mu.Unlock()
		return nil, fmt.Errorf("too many background tasks (4 active / 64 retained)")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t := &task{ID: id, Started: time.Now(), Output: &boundedOutput{}, Cancel: cancel}
	b.tasks[id] = t
	b.mu.Unlock()
	go func() {
		defer cancel()
		err := b.command(ctx, a, clamp(a.TimeoutSeconds, 600, 3600), t.Output)
		t.mu.Lock()
		defer t.mu.Unlock()
		t.Done = true
		if err != nil {
			t.Error = err.Error()
		}
	}()
	return map[string]any{"id": id, "status": "running", "timeout_seconds": clamp(a.TimeoutSeconds, 600, 3600)}, nil
}
func (b *bridge) taskStatus(id string) (any, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []map[string]any{}
	for key, t := range b.tasks {
		if id != "" && key != id {
			continue
		}
		t.mu.Lock()
		entry := map[string]any{"id": t.ID, "started_at": t.Started.UTC().Format(time.RFC3339), "done": t.Done, "error": t.Error}
		if id != "" {
			entry["output"], entry["truncated"] = t.Output.snapshot()
		}
		t.mu.Unlock()
		out = append(out, entry)
	}
	if id != "" {
		if len(out) == 0 {
			return nil, fmt.Errorf("task not found; a bridge restart clears task records")
		}
		return out[0], nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i]["started_at"].(string) < out[j]["started_at"].(string) })
	return map[string]any{"tasks": out}, nil
}
func (b *bridge) cancelTasks() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range b.tasks {
		t.Cancel()
	}
}

// 数一下还在跑的后台任务，证书续期重启前要看
func (b *bridge) activeTasks() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	active := 0
	for _, t := range b.tasks {
		t.mu.Lock()
		if !t.Done {
			active++
		}
		t.mu.Unlock()
	}
	return active
}
func (b *bridge) path(raw string) (string, error) {
	if raw == "" || len(raw) > 4096 || strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("invalid server path")
	}
	if !filepath.IsAbs(raw) {
		raw = filepath.Join(b.cfg.Home, raw)
	}
	path, err := filepath.EvalSymlinks(filepath.Clean(raw))
	if err != nil {
		return "", fmt.Errorf("path unavailable: %w", err)
	}
	if err := b.checkPath(path); err != nil {
		return "", err
	}
	return path, nil
}
func (b *bridge) checkPath(path string) error {
	for _, prefix := range []string{"/proc", "/sys", "/dev", stateDefault, b.state} {
		if prefix == "" {
			continue
		}
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return fmt.Errorf("system/credential path is blocked")
		}
	}
	for _, part := range strings.Split(path, "/") {
		lower := strings.ToLower(part)
		if lower == ".ssh" || lower == ".aws" || lower == ".gnupg" || strings.HasPrefix(lower, ".env") || lower == "id_rsa" || lower == "id_ed25519" || lower == "shadow" || lower == "key.pem" || lower == "ca-key.pem" || lower == "config.json" && strings.Contains(path, "ops-bridge") {
			return fmt.Errorf("credential files are blocked")
		}
	}
	return nil
}
func (b *bridge) readFile(a args, download bool) (any, error) {
	path, err := b.path(a.Path)
	if err != nil {
		return nil, err
	}
	f, err := secureOpen(path, false)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("only regular files are allowed")
	}
	if download {
		if info.Size() > fileLimit {
			return nil, fmt.Errorf("file exceeds 16 MB")
		}
		data, err := io.ReadAll(io.LimitReader(f, fileLimit+1))
		if err != nil {
			return nil, err
		}
		if len(data) > fileLimit {
			return nil, fmt.Errorf("file grew beyond 16 MB")
		}
		return map[string]any{"data": base64.StdEncoding.EncodeToString(data), "size": len(data)}, nil
	}
	if a.Offset < 0 {
		return nil, fmt.Errorf("offset must be nonnegative")
	}
	if _, err = f.Seek(a.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	limit := clamp(a.Limit, 16000, outputLimit)
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "content": string(data), "offset": a.Offset, "next_offset": a.Offset + int64(len(data)), "size": info.Size(), "truncated": a.Offset+int64(len(data)) < info.Size()}, nil
}
func (b *bridge) upload(a args) (any, error) {
	if a.Path == "" || len(a.Path) > 4096 || strings.ContainsRune(a.Path, 0) {
		return nil, fmt.Errorf("invalid destination")
	}
	if len(a.Data) > base64.StdEncoding.EncodedLen(fileLimit) {
		return nil, fmt.Errorf("file exceeds 16 MB")
	}
	dest := a.Path
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(b.cfg.Home, dest)
	}
	parent, err := b.path(filepath.Dir(dest))
	if err != nil {
		return nil, err
	}
	if err = b.checkPath(filepath.Join(parent, filepath.Base(dest))); err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(a.Data)
	if err != nil || len(data) > fileLimit {
		return nil, fmt.Errorf("invalid file data")
	}
	root, err := secureOpen(parent, true)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := createInDirectory(root, filepath.Base(dest))
	if err != nil {
		return nil, fmt.Errorf("cannot create destination (overwriting is forbidden): %w", err)
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		removeInDirectory(root, filepath.Base(dest))
		return nil, fmt.Errorf("file write failed")
	}
	return map[string]any{"ok": true, "size": len(data)}, nil
}
func (b *bridge) list(a args) (any, error) {
	path, err := b.path(a.Path)
	if err != nil {
		return nil, err
	}
	f, err := secureOpen(path, true)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(501)
	if err != nil && err != io.EOF {
		return nil, err
	}
	truncated := len(entries) > 500
	if truncated {
		entries = entries[:500]
	}
	out := []map[string]any{}
	for _, entry := range entries {
		info, err := entry.Info()
		if err == nil {
			out = append(out, map[string]any{"name": entry.Name(), "directory": entry.IsDir(), "symlink": entry.Type()&os.ModeSymlink != 0, "size": info.Size(), "mode": info.Mode().String()})
		}
	}
	return map[string]any{"path": path, "entries": out, "truncated": truncated}, nil
}
func publicIPs(ctx context.Context, host string) ([]net.IPAddr, error) {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, "/:@[]\r\n\x00") {
		return nil, fmt.Errorf("enter a public hostname or IPv4 address")
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("DNS lookup failed")
	}
	for _, address := range ips {
		ip := address.IP
		ipv4 := ip.To4()
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || (ipv4 != nil && (ipv4[0] == 0 || ipv4[0] == 100 && ipv4[1] >= 64 && ipv4[1] <= 127 || ipv4[0] >= 224)) {
			return nil, fmt.Errorf("private, reserved and link-local probe targets are forbidden")
		}
	}
	return ips, nil
}
func networkCheck(ctx context.Context, a args) (any, error) {
	if a.Port < 1 || a.Port > 65535 {
		return nil, fmt.Errorf("port must be 1–65535")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ips, err := publicIPs(ctx, a.Host)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(ips[0].IP.String(), fmt.Sprint(a.Port)))
	if err != nil {
		return map[string]any{"host": a.Host, "port": a.Port, "dns_ok": true, "tcp_reachable": false, "note": "An outbound probe cannot determine whether inbound access is blocked in other regions."}, nil
	}
	conn.Close()
	return map[string]any{"host": a.Host, "port": a.Port, "dns_ok": true, "tcp_reachable": true, "connect_ms": time.Since(start).Milliseconds()}, nil
}
func speedtest(ctx context.Context) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	transport := &http.Transport{TLSHandshakeTimeout: 5 * time.Second, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := publicIPs(ctx, host)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirect forbidden") }}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://speed.cloudflare.com/__down?bytes=8388608", nil)
	start := time.Now()
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("speed test endpoint unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("speed test endpoint returned HTTP %d", res.StatusCode)
	}
	size, err := io.Copy(io.Discard, io.LimitReader(res.Body, 8*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("speed test incomplete")
	}
	seconds := time.Since(start).Seconds()
	return map[string]any{"download_mbps": float64(size) * 8 / seconds / 1000000, "bytes": size, "seconds": seconds, "note": "Bounded download measurement; results vary with routing. No upload test."}, nil
}
func (b *bridge) operate(ctx context.Context, action string, a args) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, 125*time.Second)
	defer cancel()
	switch action {
	case "info":
		return map[string]any{"bridge_version": version, "os": runtime.GOOS, "architecture": runtime.GOARCH, "cpu_count": runtime.NumCPU(), "execution_user": b.cfg.User, "system": b.execute(ctx, args{Command: "uname -a; uptime; df -h; if command -v free >/dev/null 2>&1; then free -m; fi", TimeoutSeconds: 10})}, nil
	case "execute":
		return b.execute(ctx, a), nil
	case "list":
		return b.list(a)
	case "read":
		return b.readFile(a, false)
	case "download":
		return b.readFile(a, true)
	case "upload":
		return b.upload(a)
	case "cron_list":
		return b.cronList(), nil
	case "cron_add":
		return b.cronCreate(a)
	case "cron_delete":
		return b.cronDelete(a.ID)
	case "task_start":
		return b.startTask(a)
	case "task_status":
		return b.taskStatus(a.ID)
	case "task_cancel":
		b.mu.Lock()
		t := b.tasks[a.ID]
		b.mu.Unlock()
		if t == nil {
			return nil, fmt.Errorf("task not found")
		}
		t.Cancel()
		return map[string]bool{"cancelled": true}, nil
	case "network":
		return networkCheck(ctx, a)
	case "speedtest":
		return speedtest(ctx)
	default:
		return nil, fmt.Errorf("unknown operation; shutdown and reboot APIs do not exist")
	}
}
