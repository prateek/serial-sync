// Package filehash hashes a file's bytes, the shared answer for "do these
// bytes match?" behind artifact integrity checks and publish destination
// checks. It exists so neither side imports the other for one SHA-256.
package filehash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// Hash returns the hex SHA-256 of the file at path, or "" when the file does
// not exist. Anything that is not a regular file is an error.
func Hash(path string) (string, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("destination is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
