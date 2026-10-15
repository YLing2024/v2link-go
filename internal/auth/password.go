// Package auth 是管理端认证：口令哈希（scrypt）、自带账号会话（builtin）与网关身份（sso）。
//
// 口径逐条对齐旧实现 services/accountService.ts 与 middleware/auth.ts：
//   - 口令哈希格式 `scrypt$<saltHex>$<hashHex>`（N=16384, r=8, p=1, keyLen=64，随机 16 字节盐）；
//   - 会话 token 为 32 字节随机十六进制，TTL 由配置注入，命中滑动续期，过期即删；
//   - builtin 只认 `Authorization: Bearer` 或 cookie `v2link_session`，**忽略** `X-Auth-User`；
//   - sso 只认前置网关注入的 `X-Auth-User`，两种模式缺凭证都是 401（不 302、不 500）。
package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"

	"v2link-go/internal/ids"
	"v2link-go/internal/scrypt"
)

const (
	hashPrefix = "scrypt"
	saltBytes  = 16
	keyBytes   = 64
)

// HashPassword 生成 `scrypt$<saltHex>$<hashHex>`（每次随机盐，绝不存明文）。
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if err := fillRandom(salt); err != nil {
		return "", err
	}
	hash, err := scrypt.Key([]byte(password), salt, scrypt.DefaultN, scrypt.DefaultR, scrypt.DefaultP, keyBytes)
	if err != nil {
		return "", err
	}
	return hashPrefix + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(hash), nil
}

// VerifyPasswordHash 恒定时间比较；格式非法 / 长度不符一律 false（不抛异常）。
func VerifyPasswordHash(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 3 || parts[0] != hashPrefix {
		return false
	}
	salt, ok := decodeHexLenient(parts[1])
	if !ok || len(salt) == 0 {
		return false
	}
	expected, ok := decodeHexLenient(parts[2])
	if !ok || len(expected) == 0 {
		return false
	}
	actual, err := scrypt.Key([]byte(password), salt, scrypt.DefaultN, scrypt.DefaultR, scrypt.DefaultP, len(expected))
	if err != nil || len(actual) != len(expected) {
		return false
	}
	return constantTimeEqual(actual, expected)
}

// RandomPassword 生成随机初始口令：12 字节 base64url（16 个 URL 安全字符）。
func RandomPassword() string { return ids.RandomToken(12) }

// decodeHexLenient 复刻 node Buffer.from(hex,'hex') 的宽容语义：按两位一组解析，
// 遇到非法/不足两位即停止（而非报错）。
func decodeHexLenient(s string) ([]byte, bool) {
	out := make([]byte, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		hi, ok1 := hexVal(s[i])
		lo, ok2 := hexVal(s[i+1])
		if !ok1 || !ok2 {
			break
		}
		out = append(out, hi<<4|lo)
	}
	return out, true
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// constantTimeEqual 长度相同时恒定时间比较。
func constantTimeEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// fillRandom 填充随机字节（失败属系统级异常，无安全回退）。
func fillRandom(b []byte) error {
	if _, err := rand.Read(b); err != nil {
		return errors.New("读取随机数失败: " + err.Error())
	}
	return nil
}
