// Package scrypt 是 scrypt 口令派生函数的纯 Go 实现（只依赖标准库）。
//
// 为什么要自己实现：旧实现用 node:crypto 的 scryptSync，哈希格式为
// `scrypt$<saltHex>$<hashHex>`（N=16384, r=8, p=1, keyLen=64）。为了让老库里的口令在
// 新实现下仍可校验，必须用同一族参数；而工程约定只允许 modernc.org/sqlite 与
// go-sql-driver/mysql 两个第三方依赖，故这里按 RFC 7914 自实现，零新增依赖。
//
// 实现范围刻意收窄到 Node scryptSync 的默认档（N 为 2 的幂、r/p 正整数）；不做通用参数
// 校验的炫技，够用且可被 RFC 7914 测试向量验证（见 scrypt_test.go）。
package scrypt

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/bits"
)

// DefaultN / DefaultR / DefaultP / DefaultKeyLen 是 Node crypto.scryptSync 的默认成本参数。
const (
	DefaultN      = 16384
	DefaultR      = 8
	DefaultP      = 1
	DefaultKeyLen = 64
)

// maxInt32 是 ROMix 索引上限，防止 N 溢出成负数。
const maxInt32 = 1<<31 - 1

// Key 派生 keyLen 字节密钥。N 必须为大于 1 的 2 的幂，r、p 必须为正。
func Key(password, salt []byte, N, r, p, keyLen int) ([]byte, error) {
	if N <= 1 || N&(N-1) != 0 {
		return nil, errors.New("scrypt: N 必须是大于 1 的 2 的幂")
	}
	if r <= 0 || p <= 0 {
		return nil, errors.New("scrypt: r、p 必须为正整数")
	}
	if uint64(r)*uint64(p) >= 1<<30 || r > maxInt32/128/p {
		return nil, errors.New("scrypt: 参数过大")
	}
	blockLen := 128 * r
	b := pbkdf2SHA256(password, salt, 1, p*blockLen)
	for i := 0; i < p; i++ {
		romix(b[i*blockLen:(i+1)*blockLen], N, r)
	}
	return pbkdf2SHA256(password, b, 1, keyLen), nil
}

// pbkdf2SHA256 是 PBKDF2-HMAC-SHA256（iter 轮）。crypto/sha256 + hmac 足够，避免额外依赖。
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf[:], uint32(block))
		prf.Write(buf[:])
		u = prf.Sum(u[:0])
		t := make([]byte, hashLen)
		copy(t, u)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for x := range t {
				t[x] ^= u[x]
			}
		}
		dk = append(dk, t...)
	}
	return dk[:keyLen]
}

// romix 是 scrypt 的内存硬化混合（RFC 7914 §5）：建表 N 项后随机寻址 N 轮。
func romix(block []byte, N, r int) {
	words := 32 * r
	x := make([]uint32, words)
	for i := 0; i < words; i++ {
		x[i] = binary.LittleEndian.Uint32(block[4*i:])
	}
	v := make([]uint32, N*words)
	y := make([]uint32, words)
	for i := 0; i < N; i++ {
		copy(v[i*words:], x)
		blockMix(x, y, r)
	}
	for i := 0; i < N; i++ {
		j := integerify(x, r) % uint64(N)
		base := int(j) * words
		for k := 0; k < words; k++ {
			x[k] ^= v[base+k]
		}
		blockMix(x, y, r)
	}
	for i := 0; i < words; i++ {
		binary.LittleEndian.PutUint32(block[4*i:], x[i])
	}
}

// blockMix 对 2r 个 64 字节块做 Salsa20/8 混合并按 {偶数块在前、奇数块在后} 重排。
//
// 结果写入独立的 y（长度 32r），最后拷回 b：重排会让后续步骤的输入块还未读取就被覆盖，
// 不能原地写回。
func blockMix(b, y []uint32, r int) {
	var x [16]uint32
	copy(x[:], b[(2*r-1)*16:])
	for i := 0; i < 2*r; i++ {
		bin := b[i*16 : i*16+16]
		for j := 0; j < 16; j++ {
			x[j] ^= bin[j]
		}
		salsa208(&x)
		dest := i / 2
		if i%2 == 1 {
			dest += r
		}
		copy(y[dest*16:], x[:])
	}
	copy(b, y)
}

// integerify 取最后一个 64 字节块的前 8 字节按小端解释为无符号整数（RFC 7914 §5）。
func integerify(b []uint32, r int) uint64 {
	base := (2*r - 1) * 16
	return uint64(b[base]) | uint64(b[base+1])<<32
}

// salsa208 是 Salsa20/8 核心：8 轮（4 个双轮）后把结果与输入按字相加写回 x。
func salsa208(x *[16]uint32) {
	in := *x
	w := in
	for i := 0; i < 4; i++ {
		// 列轮
		w[4] ^= bits.RotateLeft32(w[0]+w[12], 7)
		w[8] ^= bits.RotateLeft32(w[4]+w[0], 9)
		w[12] ^= bits.RotateLeft32(w[8]+w[4], 13)
		w[0] ^= bits.RotateLeft32(w[12]+w[8], 18)
		w[9] ^= bits.RotateLeft32(w[5]+w[1], 7)
		w[13] ^= bits.RotateLeft32(w[9]+w[5], 9)
		w[1] ^= bits.RotateLeft32(w[13]+w[9], 13)
		w[5] ^= bits.RotateLeft32(w[1]+w[13], 18)
		w[14] ^= bits.RotateLeft32(w[10]+w[6], 7)
		w[2] ^= bits.RotateLeft32(w[14]+w[10], 9)
		w[6] ^= bits.RotateLeft32(w[2]+w[14], 13)
		w[10] ^= bits.RotateLeft32(w[6]+w[2], 18)
		w[3] ^= bits.RotateLeft32(w[15]+w[11], 7)
		w[7] ^= bits.RotateLeft32(w[3]+w[15], 9)
		w[11] ^= bits.RotateLeft32(w[7]+w[3], 13)
		w[15] ^= bits.RotateLeft32(w[11]+w[7], 18)
		// 行轮
		w[1] ^= bits.RotateLeft32(w[0]+w[3], 7)
		w[2] ^= bits.RotateLeft32(w[1]+w[0], 9)
		w[3] ^= bits.RotateLeft32(w[2]+w[1], 13)
		w[0] ^= bits.RotateLeft32(w[3]+w[2], 18)
		w[6] ^= bits.RotateLeft32(w[5]+w[4], 7)
		w[7] ^= bits.RotateLeft32(w[6]+w[5], 9)
		w[4] ^= bits.RotateLeft32(w[7]+w[6], 13)
		w[5] ^= bits.RotateLeft32(w[4]+w[7], 18)
		w[11] ^= bits.RotateLeft32(w[10]+w[9], 7)
		w[8] ^= bits.RotateLeft32(w[11]+w[10], 9)
		w[9] ^= bits.RotateLeft32(w[8]+w[11], 13)
		w[10] ^= bits.RotateLeft32(w[9]+w[8], 18)
		w[12] ^= bits.RotateLeft32(w[15]+w[14], 7)
		w[13] ^= bits.RotateLeft32(w[12]+w[15], 9)
		w[14] ^= bits.RotateLeft32(w[13]+w[12], 13)
		w[15] ^= bits.RotateLeft32(w[14]+w[13], 18)
	}
	for i := range x {
		x[i] = w[i] + in[i]
	}
}
