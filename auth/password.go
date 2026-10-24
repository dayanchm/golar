package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// Hasher hashes and verifies passwords.
type Hasher interface {
	Hash(password string) (string, error)
	// Verify reports whether password matches hash. It never panics on malformed hashes.
	Verify(hash, password string) bool
	// NeedsRehash reports whether hash should be replaced by a fresh Hash.
	NeedsRehash(hash string) bool
}

// MaxPasswordLength bounds password input to keep hashing cost predictable.
const MaxPasswordLength = 4096

var ErrPasswordTooLong = fmt.Errorf("auth: password exceeds %d bytes", MaxPasswordLength)

// Argon2id hashes passwords with Argon2id in PHC string format. The zero
// value uses OWASP's recommended minimum: 19 MiB memory, 2 iterations, 1 thread.
type Argon2id struct {
	Memory      uint32 // KiB
	Iterations  uint32
	Parallelism uint8
}

func (a Argon2id) params() (memory, iterations uint32, parallelism uint8) {
	memory, iterations, parallelism = a.Memory, a.Iterations, a.Parallelism
	if memory == 0 {
		memory = 19 * 1024
	}
	if iterations == 0 {
		iterations = 2
	}
	if parallelism == 0 {
		parallelism = 1
	}
	return
}

const argonKeyLength, argonSaltLength = 32, 16

func (a Argon2id) Hash(password string) (string, error) {
	if len(password) > MaxPasswordLength {
		return "", ErrPasswordTooLong
	}
	memory, iterations, parallelism := a.params()
	salt := make([]byte, argonSaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, argonKeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memory, iterations, parallelism,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

type argonHash struct {
	memory, iterations uint32
	parallelism        uint8
	salt, key          []byte
}

func parseArgon2id(hash string) (argonHash, error) {
	var h argonHash
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return h, errors.New("auth: not an argon2id hash")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return h, errors.New("auth: unsupported argon2 version")
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &h.memory, &h.iterations, &h.parallelism); err != nil {
		return h, errors.New("auth: malformed argon2id parameters")
	}
	// Refuse absurd parameters so a tampered hash cannot exhaust memory or CPU.
	if h.memory < 8*uint32(h.parallelism) || h.memory > 1<<21 || h.iterations < 1 || h.iterations > 64 || h.parallelism < 1 {
		return h, errors.New("auth: argon2id parameters out of range")
	}
	var err error
	if h.salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil || len(h.salt) < 8 {
		return h, errors.New("auth: malformed argon2id salt")
	}
	if h.key, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(h.key) < 16 || len(h.key) > 128 {
		return h, errors.New("auth: malformed argon2id key")
	}
	return h, nil
}

func (a Argon2id) Verify(hash, password string) bool {
	h, err := parseArgon2id(hash)
	if err != nil || len(password) > MaxPasswordLength {
		return false
	}
	key := argon2.IDKey([]byte(password), h.salt, h.iterations, h.memory, h.parallelism, uint32(len(h.key)))
	return subtle.ConstantTimeCompare(key, h.key) == 1
}

func (a Argon2id) NeedsRehash(hash string) bool {
	h, err := parseArgon2id(hash)
	memory, iterations, parallelism := a.params()
	return err != nil || h.memory != memory || h.iterations != iterations || h.parallelism != parallelism || len(h.key) != argonKeyLength
}

// Bcrypt hashes passwords with bcrypt, for compatibility with existing
// hashes (for example from Laravel). Cost defaults to 12. bcrypt only uses the
// first 72 bytes, so longer passwords are rejected.
type Bcrypt struct{ Cost int }

func (b Bcrypt) cost() int {
	if b.Cost == 0 {
		return 12
	}
	return b.Cost
}

func (b Bcrypt) Hash(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), b.cost())
	return string(hash), err
}

func (b Bcrypt) Verify(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (b Bcrypt) NeedsRehash(hash string) bool {
	cost, err := bcrypt.Cost([]byte(hash))
	return err != nil || cost != b.cost()
}

// Passwords hashes new passwords with Argon2id and verifies both Argon2id and
// bcrypt hashes. Bcrypt hashes report NeedsRehash so they migrate on login.
type Passwords struct {
	Argon2id Argon2id
	Bcrypt   Bcrypt
}

func isBcrypt(hash string) bool {
	return strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$")
}

func (p Passwords) Hash(password string) (string, error) { return p.Argon2id.Hash(password) }

func (p Passwords) Verify(hash, password string) bool {
	if isBcrypt(hash) {
		return p.Bcrypt.Verify(hash, password)
	}
	return p.Argon2id.Verify(hash, password)
}

func (p Passwords) NeedsRehash(hash string) bool {
	return isBcrypt(hash) || p.Argon2id.NeedsRehash(hash)
}

// DefaultHasher is used when a component's Hasher is nil.
var DefaultHasher Hasher = Passwords{}

// HashPassword hashes password with DefaultHasher.
func HashPassword(password string) (string, error) { return DefaultHasher.Hash(password) }

// VerifyPassword checks password against hash with DefaultHasher.
func VerifyPassword(hash, password string) bool { return DefaultHasher.Verify(hash, password) }

func hasher(h Hasher) Hasher {
	if h == nil {
		return DefaultHasher
	}
	return h
}
