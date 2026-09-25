// Package auth 实现 User API Key 的生成与校验（技术方案 §7.2）。
//
// 设计要点：
//   - Key 本身是 256 bit 高熵随机数，不存在字典攻击面，因此用 HMAC-SHA256(pepper, key)
//     而非 bcrypt/argon2 —— HMAC 可以直接建唯一索引查找，bcrypt 每次校验需要 ~50ms 且无法索引。
//   - 数据库只保存 HMAC 摘要与 12 位明文前缀（用于控制台展示 "sk-uft-a1B2c3...", 定位问题)，
//     完整明文只在生成时返回一次，此后不可恢复。
//   - pepper 来自 KMS/环境变量，不入库；泄露单条 key_hmac 不足以离线爆破出原文
//     （因为 pepper 未知，且原文本身是高熵随机数）。
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
)

const (
	// KeyPrefix 是对外可见的 Key 前缀，便于泄露扫描工具（如 GitHub Secret Scanning）识别。
	KeyPrefix = "sk-uft-"
	// rawEntropyBytes 是 Key 主体的随机字节数（256 bit）。
	rawEntropyBytes = 32
	// DisplayPrefixLen 是保存在数据库中、用于控制台展示的明文前缀长度（含 KeyPrefix）。
	DisplayPrefixLen = 13
)

var ErrInvalidKeyFormat = errors.New("auth: invalid api key format")

// GeneratedKey 是一次生成的结果：Raw 只返回这一次，调用方必须立刻展示给用户并且不持久化明文。
type GeneratedKey struct {
	Raw           string // 完整明文，例如 "sk-uft-3f9a...";仅此一次可见
	DisplayPrefix string // 存库用于展示，例如 "sk-uft-3f9a"
	HMAC          []byte // 存库用于查找校验
}

// GenerateAPIKey 生成一个新的 User API Key。pepper 不应为空，否则 HMAC 退化为无密钥摘要。
func GenerateAPIKey(pepper []byte) (*GeneratedKey, error) {
	if len(pepper) == 0 {
		return nil, errors.New("auth: pepper must not be empty")
	}
	buf := make([]byte, rawEntropyBytes)
	if _, err := rand.Read(buf); err != nil {
		return nil, fmt.Errorf("auth: read random: %w", err)
	}
	raw := KeyPrefix + base62Encode(buf)

	prefixLen := DisplayPrefixLen
	if len(raw) < prefixLen {
		prefixLen = len(raw)
	}

	return &GeneratedKey{
		Raw:           raw,
		DisplayPrefix: raw[:prefixLen],
		HMAC:          computeHMAC(pepper, raw),
	}, nil
}

// ComputeHMAC 对外暴露给校验路径：拿到客户端提交的明文 Key 后计算 HMAC 用于查库比对。
// 调用方应在格式不匹配时（缺少前缀）尽早拒绝，避免无意义的哈希计算/查库。
func ComputeHMAC(pepper []byte, raw string) ([]byte, error) {
	if len(raw) <= len(KeyPrefix) || raw[:len(KeyPrefix)] != KeyPrefix {
		return nil, ErrInvalidKeyFormat
	}
	return computeHMAC(pepper, raw), nil
}

func computeHMAC(pepper []byte, raw string) []byte {
	mac := hmac.New(sha256.New, pepper)
	mac.Write([]byte(raw))
	return mac.Sum(nil)
}

// EqualHMAC 常数时间比较，避免时序侧信道。
func EqualHMAC(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// base62Encode 把随机字节编码为 base62 字符串（比 base64 更适合出现在 URL/命令行/日志里，
// 不含 +、/、= 等需要转义的字符)。不要求可逆，仅用于生成展示型 Key。
func base62Encode(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	// 把字节串当作大数，反复除以 62 取余。
	num := make([]byte, len(b))
	copy(num, b)

	out := make([]byte, 0, len(b)*138/100+1) // log(256)/log(62) ≈ 1.38
	for !isZero(num) {
		var rem int
		num, rem = divmod62(num)
		out = append(out, base62Alphabet[rem])
	}
	if len(out) == 0 {
		out = append(out, base62Alphabet[0])
	}
	// 反转（除法过程从低位开始产出）
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return string(out)
}

func isZero(num []byte) bool {
	for _, b := range num {
		if b != 0 {
			return false
		}
	}
	return true
}

// divmod62 对大端字节数组表示的大数做除以 62，返回商（原地覆写）与余数。
func divmod62(num []byte) ([]byte, int) {
	rem := 0
	for i, b := range num {
		cur := rem*256 + int(b)
		num[i] = byte(cur / 62)
		rem = cur % 62
	}
	return num, rem
}
