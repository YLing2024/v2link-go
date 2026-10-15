// Package ids 生成链接 ID 与 UUID（对齐旧实现 lib/id.ts）。
package ids

import (
	"crypto/rand"
	"fmt"
)

// linkAlphabet 避开易混字符（0/O、1/l/I）；与旧实现 nanoid customAlphabet 字符表一致。
const linkAlphabet = "0123456789abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ"

// NewLinkID 生成 'lk_' + 8 位随机串。
func NewLinkID() string {
	return "lk_" + randomFromAlphabet(8)
}

func randomFromAlphabet(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败属于系统级异常，无安全回退可给；直接 panic 优于产出弱 ID。
		panic(fmt.Sprintf("读取随机数失败: %v", err))
	}
	out := make([]byte, n)
	for i, b := range buf {
		out[i] = linkAlphabet[int(b)%len(linkAlphabet)]
	}
	return string(out)
}

// RandomUUID 生成 RFC 4122 v4 UUID（对齐 crypto.randomUUID）。
func RandomUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("读取随机数失败: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RandomToken 生成 n 字节随机数据的 base64url 无填充编码（对齐 crypto.randomBytes(n).toString('base64url')）。
func RandomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("读取随机数失败: %v", err))
	}
	return base64URLNoPad(buf)
}

// base64URLNoPad 是 RFC 4648 §5 无填充编码（JS base64url 语义）。
func base64URLNoPad(src []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	var out []byte
	for i := 0; i < len(src); i += 3 {
		var b0, b1, b2 byte
		rem := len(src) - i
		b0 = src[i]
		if rem > 1 {
			b1 = src[i+1]
		}
		if rem > 2 {
			b2 = src[i+2]
		}
		out = append(out, alphabet[b0>>2])
		out = append(out, alphabet[(b0&0x03)<<4|b1>>4])
		if rem > 1 {
			out = append(out, alphabet[(b1&0x0f)<<2|b2>>6])
		}
		if rem > 2 {
			out = append(out, alphabet[b2&0x3f])
		}
	}
	return string(out)
}
