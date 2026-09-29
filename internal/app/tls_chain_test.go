package app

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type testChain struct {
	rootPEM, intPEM, leafPEM, keyPEM []byte
	intDER                           []byte
}

// newTestChain 造一条 root → intermediate → leaf，叶子是 IP 证书、带 AIA，跟 Let's Encrypt 的 IP 证书同形。
func newTestChain(t *testing.T) testChain {
	t.Helper()
	serial := int64(1)
	issue := func(tmpl *x509.Certificate, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) ([]byte, *x509.Certificate, *ecdsa.PrivateKey) {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		serial++
		tmpl.SerialNumber = big.NewInt(serial)
		tmpl.NotBefore, tmpl.NotAfter = time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
		if parent == nil {
			parent, parentKey = tmpl, key
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
		if err != nil {
			t.Fatal(err)
		}
		cert, _ := x509.ParseCertificate(der)
		return der, cert, key
	}
	encode := func(der []byte) []byte { return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}) }
	rootDER, root, rootKey := issue(&x509.Certificate{Subject: pkix.Name{CommonName: "Test Root"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}, nil, nil)
	intDER, inter, intKey := issue(&x509.Certificate{Subject: pkix.Name{CommonName: "YE1"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}, root, rootKey)
	leafDER, _, leafKey := issue(&x509.Certificate{IPAddresses: []net.IP{net.ParseIP("154.64.232.36")}, IssuingCertificateURL: []string{"http://ye1.i.lencr.test/"}}, inter, intKey)
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		t.Fatal(err)
	}
	return testChain{
		rootPEM: encode(rootDER), intPEM: encode(intDER), leafPEM: encode(leafDER), intDER: intDER,
		keyPEM: pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
}

func writeTestFile(t *testing.T, path string, parts ...[]byte) {
	t.Helper()
	var data []byte
	for _, part := range parts {
		data = append(data, part...)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// isolateChainSources 把 acme.sh 目录和 AIA 下载都换成测试替身，避免读本机真实目录或访问网络。
func isolateChainSources(t *testing.T, acmeDir string, fetch func(string) ([]byte, error)) {
	t.Helper()
	oldDirs, oldFetch := panelChainSearchDirs, panelChainFetch
	panelChainSearchDirs = func() []string { return []string{acmeDir} }
	if fetch == nil {
		fetch = func(string) ([]byte, error) { return nil, errors.New("offline") }
	}
	panelChainFetch = fetch
	t.Cleanup(func() { panelChainSearchDirs, panelChainFetch = oldDirs, oldFetch })
}

func TestLoadPanelCertificateKeepsFullChainAsIs(t *testing.T) {
	chain := newTestChain(t)
	dir := t.TempDir()
	isolateChainSources(t, filepath.Join(dir, "acme"), func(string) ([]byte, error) {
		t.Fatal("a file that already carries the chain must not trigger AIA")
		return nil, nil
	})
	certFile, keyFile := filepath.Join(dir, "fullchain.pem"), filepath.Join(dir, "privkey.pem")
	writeTestFile(t, certFile, chain.leafPEM, chain.intPEM, chain.rootPEM)
	writeTestFile(t, keyFile, chain.keyPEM)
	cert, source, incomplete, err := loadPanelCertificate(certFile, keyFile)
	if err != nil || source != "" || incomplete || len(cert.Certificate) != 3 {
		t.Fatalf("certs=%d source=%q incomplete=%v err=%v", len(cert.Certificate), source, incomplete, err)
	}
}

func TestLoadPanelCertificateCompletesLeafOnlyFileFromAcmeFullchain(t *testing.T) {
	chain := newTestChain(t)
	dir := t.TempDir()
	acme := filepath.Join(dir, "acme")
	isolateChainSources(t, acme, nil)
	// 面板指向只有叶子的 <ip>.cer，完整链在 acme.sh 目录里。
	certFile, keyFile := filepath.Join(dir, "panel", "154.64.232.36.cer"), filepath.Join(dir, "panel", "154.64.232.36.key")
	writeTestFile(t, certFile, chain.leafPEM)
	writeTestFile(t, keyFile, chain.keyPEM)
	other := newTestChain(t)
	writeTestFile(t, filepath.Join(acme, "other.example_ecc", "fullchain.cer"), other.leafPEM, other.intPEM)
	want := filepath.Join(acme, "154.64.232.36_ecc", "fullchain.cer")
	writeTestFile(t, want, chain.leafPEM, chain.intPEM, chain.rootPEM)
	cert, source, incomplete, err := loadPanelCertificate(certFile, keyFile)
	if err != nil || incomplete || source != want || len(cert.Certificate) != 3 {
		t.Fatalf("certs=%d source=%q incomplete=%v err=%v", len(cert.Certificate), source, incomplete, err)
	}
}

func TestLoadPanelCertificateCompletesFromSiblingCAFile(t *testing.T) {
	chain := newTestChain(t)
	dir := t.TempDir()
	isolateChainSources(t, filepath.Join(dir, "acme"), nil)
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeTestFile(t, certFile, chain.leafPEM)
	writeTestFile(t, keyFile, chain.keyPEM)
	writeTestFile(t, filepath.Join(dir, "ca.cer"), chain.intPEM, chain.rootPEM)
	cert, source, incomplete, err := loadPanelCertificate(certFile, keyFile)
	if err != nil || incomplete || source != filepath.Join(dir, "ca.cer") || len(cert.Certificate) != 3 {
		t.Fatalf("certs=%d source=%q incomplete=%v err=%v", len(cert.Certificate), source, incomplete, err)
	}
}

func TestLoadPanelCertificateFallsBackToAIA(t *testing.T) {
	chain := newTestChain(t)
	dir := t.TempDir()
	var fetched string
	isolateChainSources(t, filepath.Join(dir, "acme"), func(url string) ([]byte, error) {
		fetched = url
		return chain.intDER, nil // Let's Encrypt 的 AIA 返回 DER
	})
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeTestFile(t, certFile, chain.leafPEM)
	writeTestFile(t, keyFile, chain.keyPEM)
	cert, source, incomplete, err := loadPanelCertificate(certFile, keyFile)
	if err != nil || incomplete || fetched != "http://ye1.i.lencr.test/" || source != fetched || len(cert.Certificate) != 2 {
		t.Fatalf("certs=%d source=%q fetched=%q incomplete=%v err=%v", len(cert.Certificate), source, fetched, incomplete, err)
	}
}

func TestLoadPanelCertificateReportsUnfixableLeafOnlyChain(t *testing.T) {
	chain := newTestChain(t)
	dir := t.TempDir()
	isolateChainSources(t, filepath.Join(dir, "acme"), nil)
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeTestFile(t, certFile, chain.leafPEM)
	writeTestFile(t, keyFile, chain.keyPEM)
	// 签不了这片叶子的 ca 文件不能被当成链塞进去。
	writeTestFile(t, filepath.Join(dir, "ca.cer"), newTestChain(t).intPEM)
	cert, source, incomplete, err := loadPanelCertificate(certFile, keyFile)
	if err != nil || !incomplete || source != "" || len(cert.Certificate) != 1 {
		t.Fatalf("certs=%d source=%q incomplete=%v err=%v", len(cert.Certificate), source, incomplete, err)
	}
}

func TestLoadPanelCertificateLeavesSelfSignedAlone(t *testing.T) {
	chain := newTestChain(t)
	dir := t.TempDir()
	isolateChainSources(t, filepath.Join(dir, "acme"), nil)
	pair, err := tls.X509KeyPair(chain.leafPEM, chain.keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	// 用叶子的私钥自签一张，模拟用户自己生成的自签证书。
	key := pair.PrivateKey.(*ecdsa.PrivateKey)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "self"}, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writeTestFile(t, certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	writeTestFile(t, keyFile, chain.keyPEM)
	_, _, incomplete, err := loadPanelCertificate(certFile, keyFile)
	if err != nil || incomplete {
		t.Fatalf("self-signed certificate reported incomplete=%v err=%v", incomplete, err)
	}
}

func TestSubscriptionUserinfoOmitsExpireWhenNeverExpires(t *testing.T) {
	if got := subscriptionUserinfo(1, 2, 0, 0); got != "upload=1; download=2; total=0" {
		t.Fatalf("never-expiring subscription header = %q", got)
	}
	if got := subscriptionUserinfo(1, 2, 3, 1790000000123); got != "upload=1; download=2; total=3; expire=1790000000" {
		t.Fatalf("expiring subscription header = %q", got)
	}
}
