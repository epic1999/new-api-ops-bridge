// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 统一成浏览器 Host 头里的写法：域名小写、去掉结尾的点，IP 用标准格式；
// 跟训练场同一个主机名的直接拒绝，前端不会把桥密码发给网站自己
func normalizePublicHost(host, origin string) (string, error) {
	host = strings.TrimSuffix(strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]")), ".")
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	if !validHost(host) {
		return "", fmt.Errorf("provide --public-host with your public IP or domain")
	}
	site, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	siteHost := strings.ToLower(site.Hostname())
	if ip := net.ParseIP(siteHost); ip != nil {
		siteHost = ip.String()
	}
	if host == siteHost {
		return "", fmt.Errorf("--public-host must differ from the playground host; browsers never send bridge credentials to the playground itself, so use the server IP or a separate subdomain")
	}
	return host, nil
}

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
	// 浏览器发来的 Origin 主机名总是小写，存成一样的才对得上
	origin, err := validOrigin(strings.ToLower(rawOrigin))
	if err != nil {
		return err
	}
	if host, err = normalizePublicHost(host, origin); err != nil {
		return err
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
