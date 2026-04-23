package utils

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

// ─── Random utilities ───────────────────────────────────────
// Uses crypto/rand for cryptographically secure random generation
// Matches zergolf1994/server-scraper behavior

const alphanumCharset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// cryptoRandInt returns a cryptographically secure random int in [0, max).
func cryptoRandInt(max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		panic("failed to generate random number: " + err.Error())
	}
	return int(n.Int64())
}

// RandomString generates a random alphanumeric string of the given length.
// If special is true and length >= 3, a '-' and '_' are inserted at random positions.
// This is the primary function used by parsers.
func RandomString(length int, special bool) string {
	if length <= 0 {
		return ""
	}

	// Build base alphanumeric string
	b := make([]byte, length)
	for i := range b {
		b[i] = alphanumCharset[cryptoRandInt(len(alphanumCharset))]
	}
	result := string(b)

	if special && length >= 3 {
		dashPos := cryptoRandInt(length-2) + 1
		underscorePos := cryptoRandInt(length-2) + 1

		for dashPos == underscorePos {
			underscorePos = cryptoRandInt(length-2) + 1
		}

		insertAt := func(s string, index int, char string) string {
			return s[:index] + char + s[index:]
		}

		if dashPos < underscorePos {
			result = insertAt(result, dashPos, "-")
			result = insertAt(result, underscorePos+1, "_")
		} else {
			result = insertAt(result, underscorePos, "_")
			result = insertAt(result, dashPos+1, "-")
		}
	}

	return result
}

// RandomAlphaNum generates a random alphanumeric string of given length (no special chars).
func RandomAlphaNum(length int) string {
	return RandomString(length, false)
}

// RandomStringWithPrefix generates "{prefix}-{randomString}".
func RandomStringWithPrefix(prefix string, length int) string {
	if prefix == "" || length <= 0 {
		return prefix
	}
	return prefix + "-" + RandomString(length, false)
}

// RandomNumber generates a random numeric string of the given length.
func RandomNumber(length int) string {
	if length <= 0 {
		return ""
	}
	if length == 1 {
		return fmt.Sprintf("%d", cryptoRandInt(10))
	}
	if length > 15 {
		length = 15
	}

	var sb strings.Builder
	sb.Grow(length)

	// First digit: 1-9 (no leading zero)
	sb.WriteByte(byte('1' + cryptoRandInt(9)))

	// Remaining digits: 0-9
	for i := 1; i < length; i++ {
		sb.WriteByte(byte('0' + cryptoRandInt(10)))
	}

	return sb.String()
}
