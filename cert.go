// Copyright (C) 2025 QuantumNous. AGPL-3.0-or-later.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	leafCommonName   = "new-api Ops Bridge"
	caCommonName     = "new-api Ops Bridge CA"
	leafLifetime     = 365 * 24 * time.Hour
	caLifetime       = 10 * 365 * 24 * time.Hour
	renewBefore      = 30 * 24 * time.Hour
	forceRenewBefore = 7 * 24 * time.Hour
	renewMarker      = "tls-renew.pending"
	renewExitCode    = 75
)

// 返回这个错误时进程主动退出，让 systemd 重新拉起并在启动时续证
var errCertificateRenewal = errors.New("TLS certificate renewal is due; exiting so the service manager restarts the bridge and renews it")

// 状态目录里没有服务证书，直接签一张新的
var errNoCertificate = errors.New("no TLS certificate")

// 改名顺序固定：CA 先落地，服务证书最后换
var certificateFiles = []string{"ca-key.pem", "ca.pem", "key.pem", "cert.pem"}

type pemFile struct {
	name string
	data []byte
}

type tlsMaterial struct {
	pair    tls.Certificate
	leaf    *x509.Certificate
	ca      *x509.Certificate // 签发服务证书的本机 CA，老版本自签名或自定义证书时为空
	caKey   *ecdsa.PrivateKey
	managed bool // 桥自己签的证书才自动续期
	renewed bool
	newCA   bool
}

// 读现有证书，到续期窗口就重签；续不了时把还能用的旧证书和错误一起返回
func ensureCertificate(state, host string, now time.Time, force bool) (tlsMaterial, error) {
	recoverErr := recoverCertificateFiles(state)
	files := map[string][]byte{}
	for _, name := range certificateFiles {
		data, err := readPrivateFile(filepath.Join(state, name))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return tlsMaterial{}, err
		}
		files[name] = data
	}
	current, parseErr := parseCertificates(files)
	if recoverErr != nil {
		return current, recoverErr
	}
	due, err := renewalNeeded(current, parseErr, host, now, force)
	if err != nil || !due {
		return current, err
	}
	next, writes, err := issueCertificates(host, now, current)
	if err == nil {
		err = commitCertificateFiles(state, writes)
	}
	if err != nil {
		return current, err
	}
	return next, nil
}

// 证书和私钥只认 root 自己的 600 普通文件
func readPrivateFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !rootOwned(info) || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("%s must be a root-owned regular file with mode 600", filepath.Base(path))
	}
	return os.ReadFile(path)
}

// 把读到的 PEM 解析成证书材料；桥自己的证书缺了私钥也当没有证书，直接重签
func parseCertificates(files map[string][]byte) (tlsMaterial, error) {
	var m tlsMaterial
	if files["cert.pem"] == nil {
		return m, errNoCertificate
	}
	leaf, err := parsePEMCertificate(files["cert.pem"])
	if err != nil {
		return m, fmt.Errorf("cert.pem: %w", err)
	}
	m.leaf = leaf
	m.ca, m.managed = managedBy(leaf, files["ca.pem"])
	if files["key.pem"] == nil {
		if m.managed {
			return m, errNoCertificate
		}
		return m, errors.New("key.pem is missing for the custom certificate")
	}
	// CA 私钥对不上就当没有，续期时连 CA 一起换
	if key, keyErr := parsePEMKey(files["ca-key.pem"]); m.ca != nil && keyErr == nil && key.PublicKey.Equal(m.ca.PublicKey) {
		m.caKey = key
	}
	if m.pair, err = tls.X509KeyPair(files["cert.pem"], files["key.pem"]); err != nil {
		return m, fmt.Errorf("cert.pem and key.pem do not match: %w", err)
	}
	return m, nil
}

// 服务证书是不是桥自己签的：本机 CA 签的，或者老版本留下的自签名证书
func managedBy(leaf *x509.Certificate, caPEM []byte) (*x509.Certificate, bool) {
	if ca, err := parsePEMCertificate(caPEM); err == nil && ca.IsCA && strings.HasPrefix(ca.Subject.CommonName, caCommonName) && leaf.CheckSignatureFrom(ca) == nil {
		return ca, true
	}
	selfSigned := leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil
	return nil, selfSigned && leaf.Subject.CommonName == leafCommonName
}

// 判断现在要不要重签；自定义证书永远不动
func renewalNeeded(m tlsMaterial, parseErr error, host string, now time.Time, force bool) (bool, error) {
	if errors.Is(parseErr, errNoCertificate) {
		return true, nil
	}
	if m.leaf == nil {
		return false, fmt.Errorf("%w; delete cert.pem and key.pem to let the bridge issue a new certificate", parseErr)
	}
	if !m.managed {
		if parseErr != nil {
			return false, parseErr
		}
		if force {
			return false, errors.New("cert.pem is a custom certificate; renew it with its issuer, or delete cert.pem and key.pem to let the bridge manage certificates again")
		}
		return false, nil
	}
	// 桥自己的证书：配对坏了、主机对不上、进了续期窗口都要重签
	return force || parseErr != nil || m.leaf.NotAfter.Sub(now) <= renewBefore || m.leaf.VerifyHostname(host) != nil, nil
}

// 签新服务证书；原 CA 能用就接着用，这样导入过 CA 的浏览器不用重新信任
func issueCertificates(host string, now time.Time, current tlsMaterial) (tlsMaterial, []pemFile, error) {
	if current.ca != nil && current.caKey != nil && current.ca.NotAfter.Sub(now) >= leafLifetime {
		next, files, err := newLeaf(host, now, current.ca, current.caKey)
		// 名称约束管不了现在的主机就别硬用，换新 CA
		if err == nil && verifyLeaf(next.leaf, current.ca, host, now) == nil {
			return next, files, nil
		}
	}
	ca, caKey, caFiles, err := newLocalCA(host, now)
	if err != nil {
		return tlsMaterial{}, nil, err
	}
	next, files, err := newLeaf(host, now, ca, caKey)
	if err == nil {
		err = verifyLeaf(next.leaf, ca, host, now)
	}
	if err != nil {
		return tlsMaterial{}, nil, err
	}
	next.newCA = true
	return next, append(caFiles, files...), nil
}

// 本机 CA 带名称约束，只能给这台服务器签证书，私钥泄露也冒充不了别的网站
func newLocalCA(host string, now time.Time) (*x509.Certificate, *ecdsa.PrivateKey, []pemFile, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		// 名字带上主机，导入多台服务器的 CA 后在信任列表里分得清
		Subject:                     pkix.Name{CommonName: caCommonName + " " + host},
		NotBefore:                   now.Add(-time.Hour),
		NotAfter:                    now.Add(caLifetime),
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid:       true,
		IsCA:                        true,
		MaxPathLenZero:              true,
		PermittedDNSDomainsCritical: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.PermittedIPRanges = []*net.IPNet{singleAddress(ip)}
		// 空域名约束匹配所有域名，排除它就等于不许签任何域名
		template.ExcludedDNSDomains = []string{""}
	} else {
		template.PermittedDNSDomains = []string{host}
		template.ExcludedIPRanges = []*net.IPNet{{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)}, {IP: net.IPv6zero, Mask: net.CIDRMask(0, 128)}}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, nil, err
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		return nil, nil, nil, err
	}
	return ca, key, []pemFile{{"ca-key.pem", keyPEM}, {"ca.pem", encodeCertificate(der)}}, nil
}

// 用本机 CA 签一年期服务证书，不会超过 CA 自己的有效期
func newLeaf(host string, now time.Time, ca *x509.Certificate, caKey *ecdsa.PrivateKey) (tlsMaterial, []pemFile, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tlsMaterial{}, nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return tlsMaterial{}, nil, err
	}
	notAfter := now.Add(leafLifetime)
	if ca.NotAfter.Before(notAfter) {
		notAfter = ca.NotAfter
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: leafCommonName}, NotBefore: now.Add(-time.Hour), NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return tlsMaterial{}, nil, err
	}
	keyPEM, err := encodeKey(key)
	if err != nil {
		return tlsMaterial{}, nil, err
	}
	certPEM := encodeCertificate(der)
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tlsMaterial{}, nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tlsMaterial{}, nil, err
	}
	return tlsMaterial{pair: pair, leaf: leaf, ca: ca, caKey: caKey, managed: true, renewed: true}, []pemFile{{"key.pem", keyPEM}, {"cert.pem", certPEM}}, nil
}

// 签完按浏览器的方式验一遍链和主机名
func verifyLeaf(leaf, ca *x509.Certificate, host string, now time.Time) error {
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: host, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

// IPv4 按 4 字节存，跟证书里 IP SAN 的编码对得上
func singleAddress(ip net.IP) *net.IPNet {
	if v4 := ip.To4(); v4 != nil {
		return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}
}

func randomSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func encodeCertificate(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func encodeKey(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// 只取第一个证书块；用户自己的证书链第一张就是服务证书
func parsePEMCertificate(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parsePEMKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("no PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	ecKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("CA key must be ECDSA")
	}
	return ecKey, nil
}

// 先把新文件都写成 .new 并刷盘，再立标记逐个改名；中途断电下次启动按标记补完
func commitCertificateFiles(state string, files []pemFile) error {
	for _, f := range files {
		if err := writeNewPrivateFile(filepath.Join(state, f.name+".new"), f.data); err != nil {
			return err
		}
	}
	syncDirectory(state)
	if err := writeNewPrivateFile(filepath.Join(state, renewMarker), nil); err != nil {
		return err
	}
	syncDirectory(state)
	return finishCertificateFiles(state)
}

// 有标记说明新文件已写全，接着换上去；没标记说明上次没写完，残留直接删
func recoverCertificateFiles(state string) error {
	if _, err := os.Lstat(filepath.Join(state, renewMarker)); err == nil {
		return finishCertificateFiles(state)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, name := range certificateFiles {
		path := filepath.Join(state, name+".new")
		// 先看在不在再删，只读挂载时删不存在的文件也会报错
		if _, err := os.Lstat(path); err == nil {
			if err = os.Remove(path); err != nil {
				return err
			}
		}
	}
	return nil
}

func finishCertificateFiles(state string) error {
	for _, name := range certificateFiles {
		path := filepath.Join(state, name)
		if _, err := os.Lstat(path + ".new"); err != nil {
			continue
		}
		if err := os.Rename(path+".new", path); err != nil {
			return err
		}
	}
	syncDirectory(state)
	if err := os.Remove(filepath.Join(state, renewMarker)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	syncDirectory(state)
	return nil
}

// 目录也刷一下盘，断电后改名才不会丢；Windows 不支持，忽略即可
func syncDirectory(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

func fingerprint(der []byte) string { return fmt.Sprintf("%X", sha256.Sum256(der)) }

func formatTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04 MST") }

// 打印指纹、到期时间和本机 CA 位置，方便在浏览器里核对
func printCertificateInfo(state string) {
	certPEM, err := os.ReadFile(filepath.Join(state, "cert.pem"))
	if err != nil {
		return
	}
	leaf, err := parsePEMCertificate(certPEM)
	if err != nil {
		return
	}
	caPEM, _ := os.ReadFile(filepath.Join(state, "ca.pem"))
	ca, managed := managedBy(leaf, caPEM)
	renewal := "renews automatically"
	if !managed {
		renewal = "custom certificate; renew it with its issuer"
	}
	fmt.Printf("TLS SHA-256 fingerprint: %s\nTLS certificate valid until: %s (%s)\n", fingerprint(leaf.Raw), formatTime(leaf.NotAfter), renewal)
	if ca != nil {
		fmt.Printf("Local CA SHA-256 fingerprint: %s\nLocal CA certificate: %s (import it once so renewals need no new trust)\n", fingerprint(ca.Raw), filepath.Join(state, "ca.pem"))
	}
}

// 进了续期窗口且没有后台任务才重启；剩不到 7 天就不等了
func renewalDue(left time.Duration, activeTasks int) bool {
	return left <= renewBefore && (activeTasks == 0 || left <= forceRenewBefore)
}

// 证书看门狗：桥自己的证书到期前挑空闲时机退出，交给 systemd 重启时续证
func (b *bridge) watchCertificate(ctx context.Context, notAfter time.Time, autoRenew bool, restart func()) {
	var warned time.Time
	for {
		// 每小时查一次，卡在整分后 5 秒，重启十来秒就完，碰不到下一分钟的定时任务
		timer := time.NewTimer(time.Until(time.Now().Truncate(time.Minute).Add(time.Hour + 5*time.Second)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		left := time.Until(notAfter)
		if autoRenew {
			if renewalDue(left, b.activeTasks()) {
				restart()
				return
			}
			continue
		}
		// 自定义证书或续期失败只能提醒，一天一次别刷屏
		if left <= renewBefore && time.Since(warned) >= 24*time.Hour {
			warnCertificateExpiry(notAfter)
			warned = time.Now()
		}
	}
}

func warnCertificateExpiry(notAfter time.Time) {
	fmt.Printf("TLS certificate expires at %s and will not renew automatically. Run sudo new-api-ops-bridge renew-cert for bridge certificates, or renew a custom certificate with its issuer, then restart the bridge service.\n", formatTime(notAfter))
}
