// Package secretbox 实现上游 Provider Key 的信封加密（技术方案 §7.15）：
// 每条密钥一个随机 DEK（AES-256-GCM），DEK 再用 KEK 加密后存库。
// 泄露单条 ciphertext + wrapped DEK 不足以还原明文，还需要拿到 KEK
// （生产环境应来自 KMS；本地开发可用环境变量，见 config.Secrets.KEKEnv）。
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const keySize = 32 // AES-256

var ErrInvalidKEK = errors.New("secretbox: KEK must be 32 bytes (AES-256)")

// Box 持有解码后的 KEK，用于加解密上游密钥。
type Box struct {
	kek []byte
}

// NewBox 从 base64 编码的 KEK 字符串构造 Box（例如 config.Secrets.KEKEnv 指向的环境变量值）。
func NewBox(kekBase64 string) (*Box, error) {
	kek, err := base64.StdEncoding.DecodeString(kekBase64)
	if err != nil {
		return nil, fmt.Errorf("secretbox: decode KEK: %w", err)
	}
	if len(kek) != keySize {
		return nil, ErrInvalidKEK
	}
	return &Box{kek: kek}, nil
}

// Sealed 是加密后存库的两段密文：数据本身的密文，以及被 KEK 加密的 DEK。
type Sealed struct {
	Ciphertext []byte
	WrappedDEK []byte
}

// Seal 生成随机 DEK 加密 plaintext，再用 KEK 加密该 DEK。
func (b *Box) Seal(plaintext string) (*Sealed, error) {
	dek := make([]byte, keySize)
	if _, err := rand.Read(dek); err != nil {
		return nil, fmt.Errorf("secretbox: generate dek: %w", err)
	}

	ciphertext, err := gcmSeal(dek, []byte(plaintext))
	if err != nil {
		return nil, fmt.Errorf("secretbox: seal plaintext: %w", err)
	}
	wrappedDEK, err := gcmSeal(b.kek, dek)
	if err != nil {
		return nil, fmt.Errorf("secretbox: seal dek: %w", err)
	}
	return &Sealed{Ciphertext: ciphertext, WrappedDEK: wrappedDEK}, nil
}

// Open 用 KEK 先解出 DEK，再用 DEK 解出明文。任一步失败都返回错误
// （密文被篡改、或用了错误的 KEK）。
func (b *Box) Open(s *Sealed) (string, error) {
	dek, err := gcmOpen(b.kek, s.WrappedDEK)
	if err != nil {
		return "", fmt.Errorf("secretbox: open dek: %w", err)
	}
	plaintext, err := gcmOpen(dek, s.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("secretbox: open plaintext: %w", err)
	}
	return string(plaintext), nil
}

func gcmSeal(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	// nonce 前置存储，Open 时从密文头部取回。
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func gcmOpen(key, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, ct := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
