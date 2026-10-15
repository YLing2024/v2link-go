package scrypt

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// TestRFC7914Vectors 用 RFC 7914 §12 的三个测试向量验证实现正确性；第三条使用
// N=16384/r=8/p=1 —— 正是 Node crypto.scryptSync 的默认档，保证能与老库口令互认。
func TestRFC7914Vectors(t *testing.T) {
	cases := []struct {
		name     string
		password string
		salt     string
		n, r, p  int
		dkLen    int
		want     string
	}{
		{
			name:     "empty",
			password: "",
			salt:     "",
			n:        16, r: 1, p: 1, dkLen: 64,
			want: "77d6576238657b203b19ca42c18a0497f16b4844e3074ae8dfdffa3fede21442" +
				"fcd0069ded0948f8326a753a0fc81f17e8d3e0fb2e0d3628cf35e20c38d18906",
		},
		{
			name:     "password-NaCl",
			password: "password",
			salt:     "NaCl",
			n:        1024, r: 8, p: 16, dkLen: 64,
			want: "fdbabe1c9d3472007856e7190d01e9fe7c6ad7cbc8237830e77376634b373162" +
				"2eaf30d92e22a3886ff109279d9830dac727afb94a83ee6d8360cbdfa2cc0640",
		},
		{
			name:     "pleaseletmein-SodiumChloride",
			password: "pleaseletmein",
			salt:     "SodiumChloride",
			n:        16384, r: 8, p: 1, dkLen: 64,
			want: "7023bdcb3afd7348461c06cd81fd38ebfda8fbba904f8e3ea9b543f6545da1f2" +
				"d5432955613f0fcf62d49705242a9af9e61e85dc0d651e40dfcf017b45575887",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Key([]byte(tc.password), []byte(tc.salt), tc.n, tc.r, tc.p, tc.dkLen)
			if err != nil {
				t.Fatalf("Key 出错: %v", err)
			}
			if !bytes.Equal(got, mustHex(t, tc.want)) {
				t.Fatalf("向量不符:\n got %s\nwant %s", hex.EncodeToString(got), tc.want)
			}
		})
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("测试向量非十六进制: %v", err)
	}
	return b
}
