package app

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// 面板证书链补全。
//
// 浏览器和 iOS 会按叶子证书里的 AIA 地址自动下载缺失的中间证书，所以只发叶子证书的
// 面板在浏览器里看起来一切正常；但 Node.js（Mihomo Party）、Go/Dart（FlClash）不会补，
// 直接报 "unable to verify the first certificate"，订阅整个拉不下来。Let's Encrypt 的
// IP 证书走 Gen Y 体系，完整链是 叶子 + YE1/YR1 + 交叉签名，缺哪一环都不行。
//
// 所以证书文件里只有叶子时，按顺序补：
//  1. 同目录、acme.sh 目录里"第一张就是这张叶子"的 fullchain 文件 —— 带交叉签名，最完整；
//  2. 同目录的 ca/chain 文件里能验证叶子签名的那几张；
//  3. 叶子 AIA 指向的签发者证书（只补一层，好过什么都没有）。

// panelChainSearchDirs 是除证书所在目录外还要找完整链的地方；acme.sh 默认把每张证书
// 放在 ~/.acme.sh/<标识>[_ecc]/ 下。写成变量是为了单测替换。
var panelChainSearchDirs = func() []string {
	dirs := []string{"/root/.acme.sh"}
	if home, err := os.UserHomeDir(); err == nil && home != "/root" {
		dirs = append(dirs, filepath.Join(home, ".acme.sh"))
	}
	return dirs
}

// panelChainFetch 负责 AIA 下载；单测里换成桩，避免真的访问网络。
var panelChainFetch = func(url string) ([]byte, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, os.ErrNotExist
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<10))
}

var fullchainNames = []string{"fullchain.cer", "fullchain.pem", "fullchain.crt"}
var caChainNames = []string{"ca.cer", "ca.pem", "chain.pem", "chain.cer", "chain.crt"}

// panelChainStatus 记录当前在用证书的链状态，给 /api/state 读（那边每 5 秒一次，不能每次都重新加载证书、访问网络）。
type panelChainStatus struct {
	Incomplete bool
	Source     string
}

var (
	panelChainMu    sync.Mutex
	panelChainState panelChainStatus
)

func setPanelChainStatus(status panelChainStatus) {
	panelChainMu.Lock()
	panelChainState = status
	panelChainMu.Unlock()
}

func currentPanelChainStatus() panelChainStatus {
	panelChainMu.Lock()
	defer panelChainMu.Unlock()
	return panelChainState
}

// loadPanelCertificate 加载面板证书，必要时补全证书链。source 为空表示文件里的链原样可用；
// 否则是补链用到的来源。incomplete 表示补完之后仍然只有叶子证书。
func loadPanelCertificate(certFile, keyFile string) (cert tls.Certificate, source string, incomplete bool, err error) {
	cert, err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return cert, "", false, err
	}
	if len(cert.Certificate) > 1 {
		return cert, "", false, nil
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return cert, "", false, err
	}
	// 自签证书没有上级可补，本来就不指望公共客户端信任它。不用 CheckSignatureFrom：
	// 它要求签发者带 CA 标记，普通的自签叶子证书过不了。
	if bytes.Equal(leaf.RawIssuer, leaf.RawSubject) && leaf.CheckSignature(leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature) == nil {
		return cert, "", false, nil
	}
	chain, source := findPanelChain(certFile, leaf)
	if len(chain) == 0 {
		return cert, "", true, nil
	}
	for _, c := range chain {
		cert.Certificate = append(cert.Certificate, c.Raw)
	}
	return cert, source, false, nil
}

func findPanelChain(certFile string, leaf *x509.Certificate) ([]*x509.Certificate, string) {
	certDir := filepath.Dir(certFile)
	// 1. 第一张就是这张叶子的 fullchain：证书目录优先，再扫 acme.sh 的每个证书子目录。
	var candidates []string
	for _, name := range fullchainNames {
		candidates = append(candidates, filepath.Join(certDir, name))
	}
	for _, dir := range panelChainSearchDirs() {
		for _, name := range fullchainNames {
			matches, _ := filepath.Glob(filepath.Join(dir, "*", name))
			candidates = append(candidates, matches...)
		}
	}
	for _, path := range candidates {
		certs := readPEMCertificates(path)
		if len(certs) > 1 && bytes.Equal(certs[0].Raw, leaf.Raw) {
			return certs[1:], path
		}
	}
	// 2. 同目录的 ca/chain 文件：从能验证叶子签名的那张开始往后全要。
	for _, name := range caChainNames {
		path := filepath.Join(certDir, name)
		certs := readPEMCertificates(path)
		for i, c := range certs {
			if leaf.CheckSignatureFrom(c) == nil {
				return certs[i:], path
			}
		}
	}
	// 3. AIA。
	for _, url := range leaf.IssuingCertificateURL {
		data, err := panelChainFetch(url)
		if err != nil {
			continue
		}
		for _, c := range parseCertificatesAnyEncoding(data) {
			if leaf.CheckSignatureFrom(c) == nil {
				return []*x509.Certificate{c}, url
			}
		}
	}
	return nil, ""
}

func readPEMCertificates(path string) []*x509.Certificate {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseCertificatesAnyEncoding(data)
}

// parseCertificatesAnyEncoding 同时认 PEM 与 DER（Let's Encrypt 的 AIA 地址返回的是 DER）。
func parseCertificatesAnyEncoding(data []byte) []*x509.Certificate {
	var certs []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if c, err := x509.ParseCertificate(block.Bytes); err == nil {
			certs = append(certs, c)
		}
	}
	if len(certs) == 0 {
		if parsed, err := x509.ParseCertificates(data); err == nil {
			certs = parsed
		}
	}
	return certs
}
