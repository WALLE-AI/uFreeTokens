package console

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id 参数：m=64MiB, t=2, p=2——OWASP 密码存储备忘录给 argon2id 的推荐
// 基线之一。单次校验在普通服务器上耗时约几十毫秒，登录场景可接受，同时给
// 离线暴力破解足够的成本（相比 HMAC 这种给高熵随机 API Key 用的摘要方式，
// 见 internal/auth 包注释：密码是人选的、低熵的，必须用专门的慢哈希）。
const (
	argonMemoryKiB = 64 * 1024
	argonTime      = 2
	argonThreads   = 2
	argonKeyLen    = 32
	argonSaltLen   = 16
)

var (
	ErrInvalidHashFormat   = errors.New("console: invalid password hash format")
	ErrIncompatibleVersion = errors.New("console: incompatible argon2 version")
)

// HashPassword 用 argon2id 生成 PHC 字符串格式的哈希：
// $argon2id$v=19$m=65536,t=2,p=2$<base64 salt>$<base64 hash>
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("console: read salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return encodeHash(argonMemoryKiB, argonTime, argonThreads, salt, hash), nil
}

func encodeHash(memory uint32, time uint32, threads uint8, salt, hash []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
}

// VerifyPassword 常数时间校验 password 是否匹配 encodedHash（HashPassword 的
// 输出格式）。调用方在"用户不存在"分支应该改用 VerifyDummyPassword，抹平和
// "用户存在但密码错误"分支之间的响应时间差（见该函数注释）。
func VerifyPassword(password, encodedHash string) (bool, error) {
	memory, time, threads, salt, hash, err := decodeHash(encodedHash)
	if err != nil {
		return false, err
	}
	candidate := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(hash)))
	return subtle.ConstantTimeCompare(hash, candidate) == 1, nil
}

func decodeHash(encoded string) (memory, time uint32, threads uint8, salt, hash []byte, err error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, ErrInvalidHashFormat
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHashFormat
	}
	if version != argon2.Version {
		return 0, 0, 0, nil, nil, ErrIncompatibleVersion
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHashFormat
	}
	salt, err = base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHashFormat
	}
	hash, err = base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return 0, 0, 0, nil, nil, ErrInvalidHashFormat
	}
	return memory, time, threads, salt, hash, nil
}

// dummyHash 是启动时生成的一个固定合法 argon2id 哈希，专门给"邮箱不存在"这个
// 分支使用：Login 在查不到用户时仍然对 dummyHash 走一遍完整的 VerifyPassword
// 计算再返回"凭据无效"，抹平"邮箱不存在"和"邮箱存在但密码错误"两种情况的
// 响应时间差——否则攻击者可以用响应耗时的差异枚举出哪些邮箱已经注册过
// （技术方案关于登录接口的安全要求）。
var dummyHash = func() string {
	h, err := HashPassword("uft-dummy-password-for-timing-equalization")
	if err != nil {
		// 只可能是 crypto/rand 不可用，等同于这台机器本身不适合跑任何加密操作。
		panic(fmt.Sprintf("console: failed to precompute dummy password hash: %v", err))
	}
	return h
}()

// VerifyDummyPassword 见 dummyHash 的注释；返回值总是 false，调用方不应该
// 依赖它的 bool 结果，只应该依赖它花费的时间和 VerifyPassword 大致相当。
func VerifyDummyPassword(password string) {
	_, _ = VerifyPassword(password, dummyHash)
}
