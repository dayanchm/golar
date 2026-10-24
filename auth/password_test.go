package auth

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestArgon2id(t *testing.T) {
	h := Argon2id{}
	hash, err := h.Hash("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") || strings.Contains(hash, "correct horse") {
		t.Fatalf("hash format: %s", hash)
	}
	if !h.Verify(hash, "correct horse") || h.Verify(hash, "correct horse ") || h.Verify(hash, "") {
		t.Fatal("verify")
	}
	again, _ := h.Hash("correct horse")
	if again == hash {
		t.Fatal("hashes must be salted")
	}
	if h.NeedsRehash(hash) || !(Argon2id{Iterations: 3}).NeedsRehash(hash) {
		t.Fatal("needs rehash")
	}
	if _, err := h.Hash(strings.Repeat("x", MaxPasswordLength+1)); err != ErrPasswordTooLong {
		t.Fatalf("too long: %v", err)
	}
	parts := strings.Split(hash, "$")
	for name, bad := range map[string]string{
		"empty":       "",
		"garbage":     "not-a-hash",
		"wrong algo":  strings.Replace(hash, "argon2id", "argon2i", 1),
		"version":     strings.Replace(hash, "v=19", "v=16", 1),
		"huge memory": strings.Join([]string{"", parts[1], parts[2], "m=4294967295,t=2,p=1", parts[4], parts[5]}, "$"),
		"huge time":   strings.Join([]string{"", parts[1], parts[2], "m=19456,t=100000,p=1", parts[4], parts[5]}, "$"),
		"zero p":      strings.Join([]string{"", parts[1], parts[2], "m=19456,t=2,p=0", parts[4], parts[5]}, "$"),
		"bad salt":    strings.Join([]string{"", parts[1], parts[2], parts[3], "!!", parts[5]}, "$"),
		"short key":   strings.Join([]string{"", parts[1], parts[2], parts[3], parts[4], "AAAA"}, "$"),
		"extra field": hash + "$x",
	} {
		if h.Verify(bad, "correct horse") || !h.NeedsRehash(bad) {
			t.Errorf("%s: malformed hash accepted", name)
		}
	}
}

func TestPasswordsMigratesBcrypt(t *testing.T) {
	legacy, err := bcrypt.GenerateFromPassword([]byte("secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	p := Passwords{}
	if !p.Verify(string(legacy), "secret") || p.Verify(string(legacy), "Secret") {
		t.Fatal("bcrypt verify")
	}
	if !p.NeedsRehash(string(legacy)) {
		t.Fatal("bcrypt hashes must be upgraded")
	}
	modern, _ := p.Hash("secret")
	if !strings.HasPrefix(modern, "$argon2id$") || p.NeedsRehash(modern) {
		t.Fatalf("new hashes use argon2id: %s", modern)
	}
	b := Bcrypt{Cost: bcrypt.MinCost}
	hash, err := b.Hash("secret")
	if err != nil || !b.Verify(hash, "secret") || b.NeedsRehash(hash) || !(Bcrypt{}).NeedsRehash(hash) {
		t.Fatalf("bcrypt hasher: %v", err)
	}
	if _, err := b.Hash(strings.Repeat("x", 73)); err == nil {
		t.Fatal("bcrypt must reject passwords over 72 bytes instead of truncating")
	}
}
