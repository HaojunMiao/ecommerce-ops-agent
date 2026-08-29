package tool

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
)

// Cipher 使用 AES-GCM 加密工具鉴权配置中的密钥。
// 模型 API Key 只通过环境变量引用，不经过这个类型，也不会写入数据库。
type Cipher struct {
	aead cipher.AEAD
}

func NewCipher(secret []byte) (*Cipher, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("empty credential encryption key")
	}
	key := sha256.Sum256(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

func (c *Cipher) Encrypt(plain string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(plain), nil), nil
}

func (c *Cipher) Decrypt(ciphertext []byte) (string, error) {
	n := c.aead.NonceSize()
	if len(ciphertext) < n {
		return "", fmt.Errorf("invalid credential ciphertext")
	}
	plain, err := c.aead.Open(nil, ciphertext[:n], ciphertext[n:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt tool credential: %w", err)
	}
	return string(plain), nil
}
