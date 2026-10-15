// Package expiry 单点定义到期语义：expires_at 为哨兵 0 表示永久有效。
package expiry

// PermanentExpiresAt 是永久有效的哨兵值；所有比较必须经 IsPermanent。
const PermanentExpiresAt int64 = 0

// IsPermanent 判断是否永久有效（expires_at 为哨兵 0）。
func IsPermanent(expiresAt int64) bool {
	return expiresAt == PermanentExpiresAt
}
