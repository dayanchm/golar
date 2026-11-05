package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AWS Signature Version 4 for S3, as described in the "Signature Version 4
// signing process" and "Authenticating requests: Using query parameters"
// sections of the S3 API reference.

const (
	sigAlgorithm    = "AWS4-HMAC-SHA256"
	unsignedPayload = "UNSIGNED-PAYLOAD"
	emptySHA256     = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

type sigCredentials struct {
	accessKey, secretKey, sessionToken, region string
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

func (c sigCredentials) scope(date string) string {
	return date + "/" + c.region + "/s3/aws4_request"
}

func (c sigCredentials) signature(date, stringToSign string) string {
	key := hmacSHA256([]byte("AWS4"+c.secretKey), date)
	key = hmacSHA256(key, c.region)
	key = hmacSHA256(key, "s3")
	key = hmacSHA256(key, "aws4_request")
	return hex.EncodeToString(hmacSHA256(key, stringToSign))
}

// canonicalQuery sorts and encodes query parameters as SigV4 requires.
func canonicalQuery(query url.Values) string {
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		values := append([]string(nil), query[key]...)
		sort.Strings(values)
		for _, value := range values {
			parts = append(parts, escapeSegment(key)+"="+escapeSegment(value))
		}
	}
	return strings.Join(parts, "&")
}

// signedHeaderNames lists the headers to sign: host, content headers, range
// and every x-amz-* header.
func signedHeaderNames(req *http.Request) []string {
	names := []string{"host"}
	for name := range req.Header {
		lower := strings.ToLower(name)
		switch {
		case lower == "content-type", lower == "content-md5", lower == "range", strings.HasPrefix(lower, "x-amz-"):
			names = append(names, lower)
		}
	}
	sort.Strings(names)
	return names
}

func canonicalRequest(method, escapedPath, query string, req *http.Request, names []string, payloadHash string) string {
	var headers strings.Builder
	for _, name := range names {
		value := req.Host
		if name != "host" {
			value = strings.Join(req.Header.Values(name), ",")
		}
		headers.WriteString(name + ":" + strings.Join(strings.Fields(value), " ") + "\n")
	}
	if escapedPath == "" {
		escapedPath = "/"
	}
	return method + "\n" + escapedPath + "\n" + query + "\n" + headers.String() + "\n" + strings.Join(names, ";") + "\n" + payloadHash
}

// sign adds the x-amz-* and Authorization headers to req. req.Host must be
// set, and req.URL.RawPath must hold the escaped path.
func (c sigCredentials) sign(req *http.Request, payloadHash string, now time.Time) {
	now = now.UTC()
	amzDate, date := now.Format("20060102T150405Z"), now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if c.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", c.sessionToken)
	}
	names := signedHeaderNames(req)
	canonical := canonicalRequest(req.Method, req.URL.EscapedPath(), canonicalQuery(req.URL.Query()), req, names, payloadHash)
	stringToSign := sigAlgorithm + "\n" + amzDate + "\n" + c.scope(date) + "\n" + sha256Hex(canonical)
	req.Header.Set("Authorization", sigAlgorithm+" Credential="+c.accessKey+"/"+c.scope(date)+
		", SignedHeaders="+strings.Join(names, ";")+", Signature="+c.signature(date, stringToSign))
}

// presign returns u with the query parameters of a presigned GET request
// that is valid for expires.
func (c sigCredentials) presign(u *url.URL, expires time.Duration, now time.Time) string {
	now = now.UTC()
	amzDate, date := now.Format("20060102T150405Z"), now.Format("20060102")
	query := u.Query()
	query.Set("X-Amz-Algorithm", sigAlgorithm)
	query.Set("X-Amz-Credential", c.accessKey+"/"+c.scope(date))
	query.Set("X-Amz-Date", amzDate)
	query.Set("X-Amz-Expires", strconv.Itoa(int(expires/time.Second)))
	query.Set("X-Amz-SignedHeaders", "host")
	if c.sessionToken != "" {
		query.Set("X-Amz-Security-Token", c.sessionToken)
	}
	req := &http.Request{Method: http.MethodGet, Host: u.Host, Header: http.Header{}}
	canonical := canonicalRequest(http.MethodGet, u.EscapedPath(), canonicalQuery(query), req, []string{"host"}, unsignedPayload)
	stringToSign := sigAlgorithm + "\n" + amzDate + "\n" + c.scope(date) + "\n" + sha256Hex(canonical)
	signed := *u
	signed.RawQuery = canonicalQuery(query) + "&X-Amz-Signature=" + c.signature(date, stringToSign)
	return signed.String()
}
