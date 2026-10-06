// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 把签发结果转成跟状态目录一样的文件表
func filesOf(writes []pemFile, base map[string][]byte) map[string][]byte {
	files := map[string][]byte{}
	for name, data := range base {
		files[name] = data
	}
	for _, f := range writes {
		files[f.name] = f.data
	}
	return files
}

// 造一张不经过本机 CA 的自签名证书，模拟老版本或用户自己的证书
func selfSignedFiles(t *testing.T, commonName, host string, notAfter time.Time) map[string][]byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: commonName}, NotBefore: notAfter.Add(-leafLifetime), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true, DNSNames: []string{host}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{"cert.pem": encodeCertificate(der), "key.pem": keyPEM}
}

func TestIssuedCertificatesChainAndNameConstraints(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct{ host, otherDNS, otherIP string }{
		{"203.0.113.7", "evil.example", "198.51.100.1"},
		{"2001:db8::7", "evil.example", "2001:db8::8"},
		{"bridge.example", "evil.example", "203.0.113.7"},
	} {
		m, writes, err := issueCertificates(tc.host, now, tlsMaterial{})
		if err != nil || !m.newCA || !m.managed || len(writes) != 4 {
			t.Fatalf("%s: issue failed: %v", tc.host, err)
		}
		if err = verifyLeaf(m.leaf, m.ca, tc.host, now); err != nil {
			t.Fatalf("%s: chain rejected: %v", tc.host, err)
		}
		if got := m.leaf.NotAfter.Sub(now); got > leafLifetime || got < leafLifetime-time.Second {
			t.Fatalf("%s: leaf lifetime %v", tc.host, got)
		}
		// 拿同一个 CA 去签别的域名和 IP，校验必须拒绝
		for _, forged := range []string{tc.otherDNS, tc.otherIP} {
			other, _, err := newLeaf(forged, now, m.ca, m.caKey)
			if err != nil {
				t.Fatal(err)
			}
			if verifyLeaf(other.leaf, m.ca, forged, now) == nil {
				t.Fatalf("%s: CA name constraints allowed %s", tc.host, forged)
			}
		}
		// 写出去的文件能原样读回来，并认得出是桥自己的证书
		parsed, err := parseCertificates(filesOf(writes, nil))
		if err != nil || !parsed.managed || parsed.caKey == nil || !bytes.Equal(parsed.leaf.Raw, m.leaf.Raw) {
			t.Fatalf("%s: reparse failed: %v", tc.host, err)
		}
	}
}

func TestRenewalReusesLocalCAUntilItNearsExpiry(t *testing.T) {
	host := "203.0.113.7"
	start := time.Now()
	first, writes, err := issueCertificates(host, start, tlsMaterial{})
	if err != nil {
		t.Fatal(err)
	}
	files := filesOf(writes, nil)
	current, err := parseCertificates(files)
	if err != nil {
		t.Fatal(err)
	}
	// 一年快到了：只换服务证书，CA 不变，导入过 CA 的浏览器不用再操作
	later := start.Add(leafLifetime - 20*24*time.Hour)
	if due, err := renewalNeeded(current, nil, host, later, false); err != nil || !due {
		t.Fatal("renewal window not detected")
	}
	renewed, writes, err := issueCertificates(host, later, current)
	if err != nil || renewed.newCA || len(writes) != 2 || !bytes.Equal(renewed.ca.Raw, first.ca.Raw) {
		t.Fatalf("CA was not reused: %v", err)
	}
	if got := renewed.leaf.NotAfter.Sub(later); got > leafLifetime || got < leafLifetime-time.Second || verifyLeaf(renewed.leaf, first.ca, host, later) != nil {
		t.Fatal("renewed leaf invalid")
	}
	// CA 剩不到一年就整体换新
	nearCAExpiry := start.Add(caLifetime - 200*24*time.Hour)
	replaced, writes, err := issueCertificates(host, nearCAExpiry, current)
	if err != nil || !replaced.newCA || len(writes) != 4 {
		t.Fatalf("CA was not replaced: %v", err)
	}
	// CA 私钥丢了也只能换新 CA
	withoutKey := filesOf(nil, files)
	delete(withoutKey, "ca-key.pem")
	current, err = parseCertificates(withoutKey)
	if err != nil || !current.managed || current.caKey != nil {
		t.Fatalf("missing CA key handling: %v", err)
	}
	if next, _, err := issueCertificates(host, later, current); err != nil || !next.newCA {
		t.Fatal("missing CA key must create a new CA")
	}
}

func TestRenewalDecisions(t *testing.T) {
	host := "bridge.example"
	now := time.Now()
	m, writes, err := issueCertificates(host, now, tlsMaterial{})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		host  string
		at    time.Time
		force bool
		want  bool
	}{
		{"fresh", host, now, false, false},
		{"just outside window", host, m.leaf.NotAfter.Add(-renewBefore - time.Minute), false, false},
		{"inside window", host, m.leaf.NotAfter.Add(-renewBefore + time.Minute), false, true},
		{"expired", host, m.leaf.NotAfter.Add(time.Hour), false, true},
		{"forced", host, now, true, true},
		{"host changed", "other.example", now, false, true},
	}
	for _, tc := range cases {
		if due, err := renewalNeeded(m, nil, tc.host, tc.at, tc.force); err != nil || due != tc.want {
			t.Fatalf("%s: due=%v err=%v", tc.name, due, err)
		}
	}
	if due, err := renewalNeeded(tlsMaterial{}, errNoCertificate, host, now, false); err != nil || !due {
		t.Fatal("missing certificate must be issued")
	}
	// 桥自己的证书缺私钥：当成没有证书重签
	files := filesOf(writes, nil)
	delete(files, "key.pem")
	if current, parseErr := parseCertificates(files); parseErr != errNoCertificate || !current.managed {
		t.Fatalf("managed certificate without key: %v", parseErr)
	}
	// 坏掉的 cert.pem 不敢乱覆盖，交给用户处理
	broken := map[string][]byte{"cert.pem": []byte("garbage"), "key.pem": files["ca-key.pem"]}
	current, parseErr := parseCertificates(broken)
	if due, err := renewalNeeded(current, parseErr, host, now, false); err == nil || due {
		t.Fatal("unparseable certificate must not be overwritten")
	}
}

func TestLegacyCertificateMigratesAndCustomCertificateIsUntouched(t *testing.T) {
	host := "bridge.example"
	now := time.Now()
	// 老版本装的自签名证书：归桥管，进窗口后换成本机 CA 签的
	legacy, err := parseCertificates(selfSignedFiles(t, leafCommonName, host, now.Add(10*24*time.Hour)))
	if err != nil || !legacy.managed || legacy.ca != nil {
		t.Fatalf("legacy certificate not recognized: %v", err)
	}
	if due, err := renewalNeeded(legacy, nil, host, now, false); err != nil || !due {
		t.Fatal("legacy certificate must renew")
	}
	if next, writes, err := issueCertificates(host, now, legacy); err != nil || !next.newCA || len(writes) != 4 {
		t.Fatalf("legacy migration failed: %v", err)
	}
	// 用户自己换的证书，就算快过期也不动，强制续期要报错
	custom := selfSignedFiles(t, "bridge.example", host, now.Add(10*24*time.Hour))
	_, ours, err := issueCertificates(host, now, tlsMaterial{})
	if err != nil {
		t.Fatal(err)
	}
	// 状态目录里还留着本机 CA，也不能把用户证书认成自己的
	withOldCA := filesOf(nil, custom)
	for _, f := range ours {
		if f.name == "ca.pem" || f.name == "ca-key.pem" {
			withOldCA[f.name] = f.data
		}
	}
	for _, files := range []map[string][]byte{custom, withOldCA} {
		current, err := parseCertificates(files)
		if err != nil || current.managed {
			t.Fatalf("custom certificate treated as managed: %v", err)
		}
		if due, err := renewalNeeded(current, nil, host, now, false); err != nil || due {
			t.Fatal("custom certificate must not renew")
		}
		if _, err := renewalNeeded(current, nil, host, now, true); err == nil {
			t.Fatal("forced renewal of a custom certificate must fail")
		}
	}
	// 自定义证书缺私钥要报错，不能顺手覆盖
	delete(custom, "key.pem")
	current, parseErr := parseCertificates(custom)
	if due, err := renewalNeeded(current, parseErr, host, now, false); err == nil || due {
		t.Fatal("custom certificate without key must be reported")
	}
}

func TestRenewalWaitsForIdleBridge(t *testing.T) {
	cases := []struct {
		left   time.Duration
		active int
		want   bool
	}{
		{renewBefore + time.Hour, 0, false},
		{renewBefore, 0, true},
		{renewBefore, 2, false},
		{forceRenewBefore, 2, true},
		{-time.Hour, 1, true},
	}
	for _, tc := range cases {
		if got := renewalDue(tc.left, tc.active); got != tc.want {
			t.Fatalf("left=%v active=%d got %v", tc.left, tc.active, got)
		}
	}
}

func TestCertificateFilesCommitAndRecover(t *testing.T) {
	state := t.TempDir()
	_, writes, err := issueCertificates("203.0.113.7", time.Now(), tlsMaterial{})
	if err != nil {
		t.Fatal(err)
	}
	if err = commitCertificateFiles(state, writes); err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		data, _ := os.ReadFile(filepath.Join(state, name))
		return data
	}
	for _, f := range writes {
		if !bytes.Equal(read(f.name), f.data) {
			t.Fatalf("%s not committed", f.name)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(state, "*.new"))
	if len(leftovers) != 0 || read(renewMarker) != nil {
		t.Fatal("commit left temporary files")
	}
	// 写到一半断电：没有标记，残留 .new 要删掉，正式文件不动
	original := read("cert.pem")
	if err = writeNewPrivateFile(filepath.Join(state, "cert.pem.new"), []byte("partial")); err != nil {
		t.Fatal(err)
	}
	if err = recoverCertificateFiles(state); err != nil || !bytes.Equal(read("cert.pem"), original) || read("cert.pem.new") != nil {
		t.Fatal("incomplete renewal was not discarded")
	}
	// 改名改到一半断电：有标记，剩下的 .new 要补着换上去
	_, next, err := issueCertificates("203.0.113.7", time.Now(), tlsMaterial{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range next {
		if err = writeNewPrivateFile(filepath.Join(state, f.name+".new"), f.data); err != nil {
			t.Fatal(err)
		}
	}
	if err = writeNewPrivateFile(filepath.Join(state, renewMarker), nil); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(filepath.Join(state, "ca-key.pem.new"), filepath.Join(state, "ca-key.pem")); err != nil {
		t.Fatal(err)
	}
	if err = recoverCertificateFiles(state); err != nil {
		t.Fatal(err)
	}
	for _, f := range next {
		if !bytes.Equal(read(f.name), f.data) {
			t.Fatalf("%s not recovered", f.name)
		}
	}
	if _, err = os.Stat(filepath.Join(state, renewMarker)); !os.IsNotExist(err) {
		t.Fatal("marker not removed")
	}
}
