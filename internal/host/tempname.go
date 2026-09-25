package host

import (
	"crypto/rand"
	"math/big"
	"strings"
)

const tempChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// TempName is a name as mktemp makes them from a template: the trailing X's of pattern replaced by
// random letters and digits ("tmp.XXXXXXXXXX", ".restore-XXXXXX"). Implementations of
// FS.MkdirTemp create it exclusively and retry on a clash.
func TempName(pattern string) (string, error) {
	base := strings.TrimRight(pattern, "X")
	b := []byte(base)
	for range len(pattern) - len(base) {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(tempChars))))
		if err != nil {
			return "", err
		}
		b = append(b, tempChars[n.Int64()])
	}
	return string(b), nil
}
