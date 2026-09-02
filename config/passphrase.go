package config

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

const (
	passphraseAlgo = "pbkdf2-sha256"
	passphraseIter = 600000
	passphraseSalt = 16
	passphraseKey  = 32
)

// HashPassphrase derives a salted PBKDF2 hash for the given passphrase.
// Format uses ':' separators (no '$') so os.ExpandEnv, the config file's
// value mapper, cannot mangle it when the value is read back.
func HashPassphrase(pw string) string {
	salt := make([]byte, passphraseSalt)
	if _, err := rand.Read(salt); err != nil {
		return ""
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, passphraseIter, passphraseKey)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s:%d:%s:%s", passphraseAlgo, passphraseIter, hex.EncodeToString(salt), hex.EncodeToString(key))
}

// VerifyPassphrase reports whether pw matches the encoded hash.
func VerifyPassphrase(pw, encoded string) bool {
	parts := strings.Split(encoded, ":")
	if len(parts) != 4 || parts[0] != passphraseAlgo {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1 {
		return false
	}
	salt, err := hex.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
