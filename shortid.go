package ws

import (
	"crypto/rand"
)

// this is loosely based on Henrique Vicente / Tom Scott solution:
//   https://henvic.dev/posts/uuid/

const (
	shortIDAlphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz" // base58
	shortIDLength   = 11
)

// NewID generates a short random base-58 ID.
func NewID() string {
	b := make([]byte, shortIDLength)
	// var id = make([]byte, shortIDLength)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	for i, p := range b {
		b[i] = shortIDAlphabet[int(p)%len(shortIDAlphabet)] // discard everything but the least significant bits
	}
	return string(b)
}
