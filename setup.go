// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	return len(host) <= 253 && regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]*[a-zA-Z0-9])?$`).MatchString(host) && !strings.Contains(host, "..")
}
func initialize(state, rawOrigin, host, username string) error {
	if err := requireRoot(); err != nil {
		return err
	}
	origin, err := validOrigin(rawOrigin)
	if err != nil {
		return err
	}
	if !validHost(host) {
		return fmt.Errorf("provide --public-host with your public IP or domain")
	}
	u, err := user.Lookup(username)
	if err != nil || u.Uid == "0" {
		return fmt.Errorf("create a dedicated non-root user before initialization")
	}
	if !filepath.IsAbs(state) {
		return fmt.Errorf("state path must be absolute")
	}
	if err = validateStateAncestors(state); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(state, "config.json")); !os.IsNotExist(err) {
		return fmt.Errorf("already initialized; use credentials or rotate, never overwrite credentials")
	}
	if err = os.MkdirAll(state, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(state)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state must be a real directory")
	}
	if err = os.Chmod(state, 0700); err != nil {
		return err
	}
	if err = validateState(state); err != nil {
		return err
	}
	password, err := randomPassword()
	if err != nil {
		return err
	}
	port := 0
	for i := 0; i < 20; i++ {
		p, err := randomPort()
		if err != nil {
			return err
		}
		l, err := net.Listen("tcp", fmt.Sprintf(":%d", p))
		if err == nil {
			port = p
			l.Close()
			break
		}
	}
	if port == 0 {
		return fmt.Errorf("cannot allocate a random port")
	}
	// 首次安装直接签本机 CA 和服务证书；上次装到一半留下的有效证书会接着用
	if _, err = ensureCertificate(state, host, time.Now(), false); err != nil {
		return err
	}
	c := config{Password: password, Port: port, Origin: origin, PublicHost: host, User: username, Home: u.HomeDir}
	if err = writeConfig(state, c); err != nil {
		return err
	}
	printCredentials(c, state)
	return nil
}
func printCredentials(c config, state string) {
	fmt.Printf("\nBridge URL: https://%s\nBridge password: %s\nAllowed browser origin: %s\nExecution user: %s\n", net.JoinHostPort(c.PublicHost, fmt.Sprint(c.Port)), c.Password, c.Origin, c.User)
	printCertificateInfo(state)
}

func writeNewPrivateFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}
