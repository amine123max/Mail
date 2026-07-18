package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Box struct {
	key  []byte
	aead cipher.AEAD
}

var hexadecimalKey = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

func New(dataDir, configuredKey string, production bool) (*Box, error) {
	key, err := loadMasterKey(dataDir, configuredKey, production)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{key: key, aead: aead}, nil
}

func (b *Box) Encrypt(value string) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nil, nonce, []byte(value), nil)
	tagSize := b.aead.Overhead()
	if len(sealed) < tagSize {
		return "", errors.New("加密结果无效")
	}
	ciphertext := sealed[:len(sealed)-tagSize]
	tag := sealed[len(sealed)-tagSize:]
	return strings.Join([]string{
		"v1",
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(tag),
		base64.StdEncoding.EncodeToString(ciphertext),
	}, ":"), nil
}

func (b *Box) Decrypt(value string) (string, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", errors.New("数据库中的加密凭据格式无效")
	}
	nonce, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", errors.New("数据库中的加密凭据格式无效")
	}
	tag, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", errors.New("数据库中的加密凭据格式无效")
	}
	ciphertext, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return "", errors.New("数据库中的加密凭据格式无效")
	}
	opened, err := b.aead.Open(nil, nonce, append(ciphertext, tag...), nil)
	if err != nil {
		return "", errors.New("无法解密数据库中的凭据")
	}
	return string(opened), nil
}

func (b *Box) BlindIndex(value string) string {
	mac := hmac.New(sha256.New, b.key)
	_, _ = mac.Write([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(mac.Sum(nil))
}

func loadMasterKey(dataDir, configured string, production bool) ([]byte, error) {
	if strings.TrimSpace(configured) != "" {
		return parseKey(configured)
	}
	if production {
		return nil, errors.New("生产环境必须通过 MAIL_ENCRYPTION_KEY 提供外部加密密钥")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	keyFile := filepath.Join(dataDir, ".master-key")
	if raw, err := os.ReadFile(keyFile); err == nil {
		key, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if decodeErr != nil || len(key) != 32 {
			return nil, errors.New("data/.master-key 内容无效，无法解密已有邮箱账号")
		}
		return key, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

func parseKey(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	var key []byte
	var err error
	if hexadecimalKey.MatchString(trimmed) {
		key, err = hex.DecodeString(trimmed)
	} else {
		key, err = base64.StdEncoding.DecodeString(trimmed)
	}
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("MAIL_ENCRYPTION_KEY 必须是 32 字节的 Base64 或 64 位十六进制字符串")
	}
	return key, nil
}
