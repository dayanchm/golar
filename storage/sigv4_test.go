package storage

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The expected signatures come from the worked examples in the Amazon S3 API
// reference ("Signature Calculations for the Authorization Header" and
// "Authenticating Requests: Using Query Parameters"), which use these
// documentation-only credentials.
var exampleCredentials = sigCredentials{
	accessKey: "AKIAIOSFODNN7EXAMPLE",
	secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	region:    "us-east-1",
}

var exampleTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func TestSigV4HeaderExample(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	req.Header.Set("Range", "bytes=0-9")
	exampleCredentials.sign(req, emptySHA256, exampleTime)
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;range;x-amz-content-sha256;x-amz-date, " +
		"Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	if got := req.Header.Get("Authorization"); got != want {
		t.Errorf("Authorization =\n%s\nwant\n%s", got, want)
	}
}

func TestSigV4PresignExample(t *testing.T) {
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/test.txt")
	got := exampleCredentials.presign(u, 24*time.Hour, exampleTime)
	if !strings.HasSuffix(got, "&X-Amz-Signature=aeeed9bbccd4d02ee5c0109b86d86835f995330da4c265957d157751f604d404") {
		t.Errorf("presigned URL = %s", got)
	}
	if !strings.Contains(got, "X-Amz-Credential=AKIAIOSFODNN7EXAMPLE%2F20130524%2Fus-east-1%2Fs3%2Faws4_request") ||
		!strings.Contains(got, "X-Amz-Expires=86400") {
		t.Errorf("presigned query = %s", got)
	}
}

func TestSigV4SessionToken(t *testing.T) {
	c := exampleCredentials
	c.sessionToken = "token/with+chars"
	req, _ := http.NewRequest(http.MethodGet, "https://examplebucket.s3.amazonaws.com/a.txt", nil)
	c.sign(req, emptySHA256, exampleTime)
	if req.Header.Get("X-Amz-Security-Token") != c.sessionToken || !strings.Contains(req.Header.Get("Authorization"), "x-amz-security-token") {
		t.Errorf("headers = %v", req.Header)
	}
	u, _ := url.Parse("https://examplebucket.s3.amazonaws.com/a.txt")
	if got := c.presign(u, time.Minute, exampleTime); !strings.Contains(got, "X-Amz-Security-Token=token%2Fwith%2Bchars") {
		t.Errorf("presigned = %s", got)
	}
}
