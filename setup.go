// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
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
	if err = createCertificate(state, host); err != nil {
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
	cert, err := os.ReadFile(filepath.Join(state, "cert.pem"))
	if err == nil {
		block, _ := pem.Decode(cert)
		if block != nil {
			fmt.Printf("TLS SHA-256 fingerprint: %X\n", sha256.Sum256(block.Bytes))
		}
	}
}
func createCertificate(state, host string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "new-api Ops Bridge"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err = writeNewPrivateFile(filepath.Join(state, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return err
	}
	return writeNewPrivateFile(filepath.Join(state, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
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
