// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
//go:build linux

package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestFilePolicyUploadAndDownload(t *testing.T) {
	b := testBridge(t)
	b.state = filepath.Join(b.cfg.Home, "state")
	if err := os.Mkdir(b.state, 0700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".ssh/key", ".env", "key.pem", "state/config.json"} {
		if _, err := b.upload(args{Path: p, Data: base64.StdEncoding.EncodeToString([]byte("secret"))}); err == nil {
			t.Fatalf("uploaded protected %s", p)
		}
	}
	a := args{Path: "normal.txt", Data: base64.StdEncoding.EncodeToString([]byte("hello 中文"))}
	if _, err := b.upload(a); err != nil {
		t.Fatal(err)
	}
	if _, err := b.upload(a); err == nil {
		t.Fatal("overwrite")
	}
	v, err := b.readFile(args{Path: a.Path}, true)
	if err != nil {
		t.Fatal(err)
	}
	if v.(map[string]any)["data"] != a.Data {
		t.Fatal("download corrupted")
	}
	if _, err = b.readFile(args{Path: a.Path, Offset: -1}, false); err == nil {
		t.Fatal("negative offset")
	}
	if err = os.Symlink(filepath.Join(b.cfg.Home, ".env"), filepath.Join(b.cfg.Home, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err = b.readFile(args{Path: "alias"}, false); err == nil {
		t.Fatal("credential symlink")
	}
}
func TestFIFODoesNotBlockFileOperations(t *testing.T) {
	b := testBridge(t)
	path := filepath.Join(b.cfg.Home, "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"read", "download", "list"} {
		done := make(chan error, 1)
		go func() { _, err := b.operate(context.Background(), action, args{Path: path}); done <- err }()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("FIFO accepted")
			}
		case <-time.After(time.Second):
			t.Fatalf("%s blocked on FIFO", action)
		}
	}
}
func TestConcurrentSymlinkReplacementNeverReadsSecret(t *testing.T) {
	b := testBridge(t)
	secret := filepath.Join(b.cfg.Home, ".env")
	if err := os.WriteFile(secret, []byte("PROTECTED_VALUE"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(b.cfg.Home, "normal")
	safe := filepath.Join(b.cfg.Home, "replacement")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(target)
			_ = os.Symlink(secret, target)
			_ = os.WriteFile(safe, []byte("normal"), 0600)
			_ = os.Rename(safe, target)
		}
	}()
	for i := 0; i < 1000; i++ {
		v, err := b.readFile(args{Path: target}, false)
		if err == nil && strings.Contains(v.(map[string]any)["content"].(string), "PROTECTED_VALUE") {
			close(stop)
			wg.Wait()
			t.Fatal("symlink bypass")
		}
	}
	close(stop)
	wg.Wait()
}
func TestStateRejectsForeignOwnersWritableAncestorsAndSymlinks(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root setup test")
	}
	state := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := validateState(state); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(state, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if validateState(state) == nil {
		t.Fatal("foreign state accepted")
	}
	_ = os.Chown(state, 0, 0)
	parent := filepath.Dir(state)
	_ = os.Chmod(parent, 0777)
	if validateState(state) == nil {
		t.Fatal("writable ancestor accepted")
	}
	_ = os.Chmod(parent, 0700)
	alias := filepath.Join(parent, "alias")
	_ = os.Symlink(state, alias)
	if validateState(alias) == nil {
		t.Fatal("state symlink accepted")
	}
	c := bConfigForTest(t)
	if err := writeConfig(state, c); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(state, "config.json")
	_ = os.Chown(file, 65534, 65534)
	if _, err := readConfig(state); err == nil {
		t.Fatal("foreign config owner accepted")
	}
}
func bConfigForTest(t *testing.T) config { return testBridge(t).cfg }
func TestManagedCronPersistsAndDeletesWithoutPrivilegedHelpers(t *testing.T) {
	b := testBridge(t)
	v, err := b.cronCreate(args{Schedule: "0 3 * * *", Command: "printf test"})
	if err != nil {
		t.Fatal(err)
	}
	id := v.(map[string]any)["job"].(cronJob).ID
	other := newBridge(b.cfg)
	if err = other.loadCron(); err != nil || len(other.cronJobs) != 1 {
		t.Fatalf("reload %v", err)
	}
	if _, err = other.cronDelete(id); err != nil {
		t.Fatal(err)
	}
	last := newBridge(b.cfg)
	if err = last.loadCron(); err != nil || len(last.cronJobs) != 0 {
		t.Fatal("delete persistence")
	}
	if _, err = b.cronCreate(args{Schedule: "0 99 * * *", Command: "echo test"}); err == nil {
		t.Fatal("bad schedule persisted")
	}
}

func TestCronDispatchStartsTrackedTaskAndReportsCapacity(t *testing.T) {
	b := testBridge(t)
	for i := 0; i < 5; i++ {
		if _, err := b.cronCreate(args{Schedule: "* * * * *", Command: "sleep 20"}); err != nil {
			t.Fatal(err)
		}
	}
	b.dispatchCron(append([]cronJob{}, b.cronJobs...), time.Now())
	defer b.cancelTasks()
	if len(b.tasks) != 4 || b.cronJobs[4].LastError == "" || b.cronJobs[0].LastTaskID == "" {
		t.Fatal("cron capacity/status")
	}
}
func TestShellCleanEnvironmentTimeoutAndTaskCancellation(t *testing.T) {
	b := testBridge(t)
	t.Setenv("BRIDGE_PRIVATE_TEST", "must-not-be-inherited")
	v := b.execute(context.Background(), args{Command: "printf '%s' \"${BRIDGE_PRIVATE_TEST-unset}\"; head -c 70000 /dev/zero"}).(map[string]any)
	if !v["ok"].(bool) || !v["truncated"].(bool) || strings.Contains(v["output"].(string), "must-not-be-inherited") {
		t.Fatal("environment/output isolation")
	}
	start := time.Now()
	v = b.execute(context.Background(), args{Command: "sleep 20", TimeoutSeconds: 1}).(map[string]any)
	if v["ok"].(bool) || time.Since(start) > 3*time.Second {
		t.Fatal("timeout")
	}
	vTask, err := b.startTask(args{Command: "sleep 20"})
	if err != nil {
		t.Fatal(err)
	}
	id := vTask.(map[string]any)["id"].(string)
	if _, err = b.operate(context.Background(), "task_cancel", args{ID: id}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(3 * time.Second); ; {
		v, err := b.taskStatus(id)
		if err != nil {
			t.Fatal(err)
		}
		if v.(map[string]any)["done"].(bool) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("task not cancelled")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestPrivateNetworkTargetsAreRejected(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "192.168.1.1", "evil/path"} {
		if _, err := publicIPs(context.Background(), host); err == nil {
			t.Fatalf("accepted %s", host)
		}
	}
}
