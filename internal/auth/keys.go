package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	KeyPrefix = "gw-"
	keyLength = 32
)

// GenerateKey создаёт новый виртуальный ключ в формате gw-<random>.
func GenerateKey() (string, error) {
	b := make([]byte, keyLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate key: %w", err)
	}
	// Используем hex, чтобы ключ был безопасным для URL и заголовков.
	return KeyPrefix + hex.EncodeToString(b), nil
}

// HashKey возвращает SHA-256 хеш ключа в hex-формате.
// В БД хранится только хеш, не plaintext.
func HashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// PrefixAndLast4 возвращает префикс (gw_) и последние 4 символа ключа.
// Используется для отображения в админке: "gw_ ... abcd".
func PrefixAndLast4(key string) (prefix, last4 string) {
	// Убираем "gw-" для префикса, оставляем "gw" как есть.
	prefix = KeyPrefix
	if len(key) >= 4 {
		last4 = key[len(key)-4:]
	} else {
		last4 = key
	}
	return prefix, last4
}

// IsValidKeyFormat проверяет, что строка похожа на ключ (начинается с gw- и достаточно длинная).
func IsValidKeyFormat(key string) bool {
	return strings.HasPrefix(key, KeyPrefix) && len(key) > len(KeyPrefix)+8
}
