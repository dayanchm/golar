package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"time"
)

// signer creates and checks the expires and signature query parameters of
// local temporary URLs.
type signer struct{ key []byte }

func (s *signer) mac(name string, expires int64) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte("golar-storage-v1\n" + name + "\n" + strconv.FormatInt(expires, 10)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

func (s *signer) sign(name string, expires time.Time) string {
	unix := expires.Unix()
	return url.Values{"expires": {strconv.FormatInt(unix, 10)}, "signature": {s.mac(name, unix)}}.Encode()
}

func (s *signer) valid(name string, query url.Values, now time.Time) bool {
	expires, err := strconv.ParseInt(query.Get("expires"), 10, 64)
	if err != nil || now.Unix() > expires {
		return false
	}
	return hmac.Equal([]byte(query.Get("signature")), []byte(s.mac(name, expires)))
}
