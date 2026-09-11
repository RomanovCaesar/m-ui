package app

import (
	"context"
	"crypto/ecdh"
	"crypto/mlkem"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

type x25519KeyPair struct {
	PrivateKey string `json:"privateKey"`
	PublicKey  string `json:"publicKey"`
}

type vlessEncryptionPair struct {
	Authentication string `json:"authentication"`
	Decryption     string `json:"decryption"`
	Encryption     string `json:"encryption"`
}

func generateUUIDv4() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}

func generateX25519KeyPair() (x25519KeyPair, error) {
	privateBytes := make([]byte, 32)
	if _, err := rand.Read(privateBytes); err != nil {
		return x25519KeyPair{}, err
	}
	// RFC 7748 clamping. This also makes the encoded private key match the
	// format emitted by both Mihomo and Xray command-line generators.
	privateBytes[0] &= 248
	privateBytes[31] &= 127
	privateBytes[31] |= 64
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		return x25519KeyPair{}, err
	}
	return x25519KeyPair{
		PrivateKey: base64.RawURLEncoding.EncodeToString(privateBytes),
		PublicKey:  base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()),
	}, nil
}

func deriveX25519PublicKey(encodedPrivateKey string) (string, error) {
	privateBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(encodedPrivateKey))
	if err != nil {
		return "", fmt.Errorf("Reality 私钥不是有效的 Base64URL: %w", err)
	}
	if len(privateBytes) != 32 {
		return "", fmt.Errorf("Reality 私钥解码后必须为 32 字节")
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil {
		return "", fmt.Errorf("Reality 私钥无效: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes()), nil
}

func generateVLESSEncryption(authentication string) (vlessEncryptionPair, error) {
	switch strings.ToLower(strings.TrimSpace(authentication)) {
	case "x25519":
		pair, err := generateX25519KeyPair()
		if err != nil {
			return vlessEncryptionPair{}, err
		}
		return vlessEncryptionPair{
			Authentication: "x25519",
			Decryption:     "mlkem768x25519plus.native.600s." + pair.PrivateKey,
			Encryption:     "mlkem768x25519plus.native.0rtt." + pair.PublicKey,
		}, nil
	case "mlkem768", "ml-kem-768":
		seed := make([]byte, 64)
		if _, err := rand.Read(seed); err != nil {
			return vlessEncryptionPair{}, err
		}
		privateKey, err := mlkem.NewDecapsulationKey768(seed)
		if err != nil {
			return vlessEncryptionPair{}, err
		}
		seedBase64 := base64.RawURLEncoding.EncodeToString(seed)
		clientBase64 := base64.RawURLEncoding.EncodeToString(privateKey.EncapsulationKey().Bytes())
		return vlessEncryptionPair{
			Authentication: "mlkem768",
			Decryption:     "mlkem768x25519plus.native.600s." + seedBase64,
			Encryption:     "mlkem768x25519plus.native.0rtt." + clientBase64,
		}, nil
	default:
		return vlessEncryptionPair{}, fmt.Errorf("请选择 X25519 或 ML-KEM-768 Authentication")
	}
}

func normalizeInboundSecrets(inbound *Inbound) error {
	if inbound == nil {
		return nil
	}
	if inbound.Reality.Enabled && strings.TrimSpace(inbound.Reality.PrivateKey) != "" {
		publicKey, err := deriveX25519PublicKey(inbound.Reality.PrivateKey)
		if err != nil {
			return err
		}
		inbound.Reality.PublicKey = publicKey
	}
	normalizeVLESSMetadata(inbound)
	return nil
}

func normalizeVLESSMetadata(inbound *Inbound) {
	if inbound == nil || inbound.Type != "vless" || inbound.Decryption == "" || inbound.Decryption == "none" {
		return
	}
	parts := strings.Split(inbound.Decryption, ".")
	if len(parts) < 4 || parts[0] != "mlkem768x25519plus" {
		return
	}
	serverKey, err := base64.RawURLEncoding.DecodeString(parts[len(parts)-1])
	if err != nil {
		return
	}
	switch len(serverKey) {
	case 32:
		privateKey, err := ecdh.X25519().NewPrivateKey(serverKey)
		if err != nil {
			return
		}
		inbound.VLESSAuth = "x25519"
		if inbound.Encryption == "" || inbound.Encryption == "none" {
			inbound.Encryption = "mlkem768x25519plus.native.0rtt." + base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
		}
	case 64:
		privateKey, err := mlkem.NewDecapsulationKey768(serverKey)
		if err != nil {
			return
		}
		inbound.VLESSAuth = "mlkem768"
		if inbound.Encryption == "" || inbound.Encryption == "none" {
			inbound.Encryption = "mlkem768x25519plus.native.0rtt." + base64.RawURLEncoding.EncodeToString(privateKey.EncapsulationKey().Bytes())
		}
	}
}

func (a *App) handleNewUUID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	value, err := generateUUIDv4()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]string{"uuid": value}})
}

func (a *App) handleRealityKeyPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	pair, err := generateX25519KeyPair()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, apiResponse{Message: a.tr(err.Error())})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: pair})
}

func (a *App) handleVLESSEncryption(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct {
		Authentication string `json:"authentication"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid request"})
		return
	}
	pair, err := generateVLESSEncryption(input.Authentication)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(err.Error())})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: pair})
}

func (a *App) handleECHKeyPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse{Message: "method not allowed"})
		return
	}
	var input struct {
		ServerName string `json:"serverName"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: "invalid request"})
		return
	}
	serverName := strings.TrimSpace(input.ServerName)
	if serverName == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("ECH 需要填写 SNI")})
		return
	}
	a.manager.mu.Lock()
	corePath := strings.TrimSpace(a.manager.state.Settings.CorePath)
	a.manager.mu.Unlock()
	if corePath == "" {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("尚未配置 Mihomo 核心路径")})
		return
	}
	if _, err := os.Stat(corePath); err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr(fmt.Sprintf("Mihomo 核心不可用: %v", err))})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, corePath, "generate", "ech-keypair", serverName).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("ECH 密钥生成失败: " + message)})
		return
	}
	text := string(output)
	const begin, end = "-----BEGIN ECH KEYS-----", "-----END ECH KEYS-----"
	start, finish := strings.Index(text, begin), strings.Index(text, end)
	if start < 0 || finish < start {
		writeJSON(w, http.StatusBadRequest, apiResponse{Message: a.tr("Mihomo 未返回有效的 ECH 密钥")})
		return
	}
	key := strings.TrimSpace(text[start : finish+len(end)])
	config := ""
	if line := strings.Index(text, "Config:"); line >= 0 {
		config = strings.TrimSpace(strings.SplitN(text[line+len("Config:"):], "\n", 2)[0])
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apiResponse{OK: true, Data: map[string]string{"key": key, "config": config, "serverName": serverName}})
}
