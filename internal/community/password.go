package community

import (
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const MinPasswordLength = 15

// OWASP's Argon2id minimum: 19 MiB, two passes, one lane. The HTTP layer also
// bounds simultaneous hash operations so sign-in cannot exhaust container RAM.
func passwordKey(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, 2, 19*1024, 1, 32)
}
func newAccount(id, name, password string) Account {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	return Account{User: User{ID: id, Name: name}, Salt: salt, Hash: passwordKey(password, salt), HashVersion: 1}
}

// NewAdministrator is for trusted provisioning tools, never public registration.
// Newly provisioned administrators follow the public registration password policy.
func NewAdministrator(password string) (Account, error) {
	if !validPassword(password) {
		return Account{}, fmt.Errorf("administrator password must contain %d to 128 characters", MinPasswordLength)
	}
	a := newAccount("admin", "管理者", password)
	a.User.Admin = true
	return a, nil
}
func checkPassword(a Account, password string) bool {
	if a.HashVersion != 1 || len(a.Salt) != 16 || len(a.Hash) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(passwordKey(password, a.Salt), a.Hash) == 1
}
func validPassword(password string) bool {
	return utf8.ValidString(password) && utf8.RuneCountInString(password) >= MinPasswordLength && utf8.RuneCountInString(password) <= 128 && len(password) <= 512
}
func validUsername(id string) bool {
	if len(id) < 3 || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}
func normalizeUsername(id string) string { return strings.ToLower(strings.TrimSpace(id)) }
