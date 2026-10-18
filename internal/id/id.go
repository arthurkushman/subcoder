package id

import (
	"crypto/rand"
	"encoding/hex"
)

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

func New(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}

	out := make([]byte, 10)
	for i := 0; i < 10; i++ {
		out[i] = alphabet[int(b[i%len(b)])%len(alphabet)]
	}

	return prefix + "_" + string(out)
}

func Hex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
