package host

import (
	"crypto/rand"
	"math/big"
)

const tempChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// TempName is a name as `mktemp -d` makes them: tmp. and 10 random letters and digits.
// Implementations of FS.MkdirTemp create it exclusively and retry on a clash.
func TempName() (string, error) {
	b := []byte("tmp.")
	for range 10 {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(tempChars))))
		if err != nil {
			return "", err
		}
		b = append(b, tempChars[n.Int64()])
	}
	return string(b), nil
}
