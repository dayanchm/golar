package storage

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeS3 is an in-memory S3 that checks every request's SigV4 signature
// with the same credentials, so a request the driver signed wrongly fails.
type fakeS3 struct {
	t       *testing.T
	creds   sigCredentials
	bucket  string
	virtual bool // bucket in the Host header instead of the path
	mu      sync.Mutex
	objects map[string]fakeObject
	pages   int // List requests served
}

type fakeObject struct {
	body        []byte
	contentType string
	modified    time.Time
}

func (f *fakeS3) verify(r *http.Request) bool {
	if sig := r.URL.Query().Get("X-Amz-Signature"); sig != "" {
		q := r.URL.Query()
		date, err := time.Parse("20060102T150405Z", q.Get("X-Amz-Date"))
		expires, _ := strconv.Atoi(q.Get("X-Amz-Expires"))
		if err != nil || time.Now().After(date.Add(time.Duration(expires)*time.Second)) {
			return false
		}
		u := &url.URL{Scheme: "http", Host: r.Host, Path: r.URL.Path, RawPath: r.URL.RawPath}
		q.Del("X-Amz-Signature")
		for _, key := range []string{"X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-SignedHeaders", "X-Amz-Security-Token"} {
			q.Del(key)
		}
		u.RawQuery = q.Encode()
		want := f.creds.presign(u, time.Duration(expires)*time.Second, date)
		return strings.HasSuffix(want, "X-Amz-Signature="+sig)
	}
	auth := r.Header.Get("Authorization")
	_, signed, ok := strings.Cut(auth, "SignedHeaders=")
	if !ok {
		return false
	}
	signed, _, _ = strings.Cut(signed, ",")
	date, err := time.Parse("20060102T150405Z", r.Header.Get("X-Amz-Date"))
	if err != nil {
		return false
	}
	clone := &http.Request{Method: r.Method, Host: r.Host, URL: r.URL, Header: http.Header{}}
	for _, name := range strings.Split(signed, ";") {
		if name != "host" {
			clone.Header[http.CanonicalHeaderKey(name)] = r.Header.Values(name)
		}
	}
	clone.Header.Del("X-Amz-Date")
	clone.Header.Del("X-Amz-Content-Sha256")
	clone.Header.Del("X-Amz-Security-Token")
	f.creds.sign(clone, r.Header.Get("X-Amz-Content-Sha256"), date)
	return clone.Header.Get("Authorization") == auth
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !f.verify(r) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `<Error><Code>SignatureDoesNotMatch</Code><Message>bad signature</Message></Error>`)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/")
	if !f.virtual {
		bucket, rest, _ := strings.Cut(key, "/")
		if bucket != f.bucket {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `<Error><Code>NoSuchBucket</Code></Error>`)
			return
		}
		key = rest
	} else if !strings.HasPrefix(r.Host, f.bucket+".") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodPut:
		if r.ContentLength < 0 {
			w.WriteHeader(http.StatusLengthRequired)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if int64(len(body)) != r.ContentLength {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.objects[key] = fakeObject{body, r.Header.Get("Content-Type"), time.Now().UTC().Truncate(time.Second)}
	case r.Method == http.MethodGet && key == "" && r.URL.Query().Get("list-type") == "2":
		f.pages++
		prefix := r.URL.Query().Get("prefix")
		var keys []string
		for k := range f.objects {
			if strings.HasPrefix(k, prefix) {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		start, _ := strconv.Atoi(r.URL.Query().Get("continuation-token"))
		end := min(start+2, len(keys)) // two keys per page exercises pagination
		type content struct {
			Key          string
			Size         int
			LastModified string
		}
		result := struct {
			XMLName               xml.Name `xml:"ListBucketResult"`
			Contents              []content
			IsTruncated           bool
			NextContinuationToken string `xml:",omitempty"`
		}{IsTruncated: end < len(keys)}
		if result.IsTruncated {
			result.NextContinuationToken = strconv.Itoa(end)
		}
		for _, k := range keys[start:end] {
			result.Contents = append(result.Contents, content{k, len(f.objects[k].body), f.objects[k].modified.Format(time.RFC3339)})
		}
		xml.NewEncoder(w).Encode(result)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		object, ok := f.objects[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			if r.Method == http.MethodGet {
				io.WriteString(w, `<Error><Code>NoSuchKey</Code><Message>missing</Message></Error>`)
			}
			return
		}
		w.Header().Set("Content-Type", object.contentType)
		w.Header().Set("Content-Length", strconv.Itoa(len(object.body)))
		w.Header().Set("Last-Modified", object.modified.Format(http.TimeFormat))
		if r.Method == http.MethodGet {
			w.Write(object.body)
		}
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func newFakeS3(t *testing.T, virtual bool) (*fakeS3, *httptest.Server) {
	f := &fakeS3{t: t, creds: sigCredentials{accessKey: "AKID", secretKey: "secret", region: "eu-central-1"}, bucket: "media", virtual: virtual, objects: map[string]fakeObject{}}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	return f, server
}

func TestS3PathStyle(t *testing.T) {
	_, server := newFakeS3(t, false)
	disk, err := NewS3(S3Config{Bucket: "media", Region: "eu-central-1", Endpoint: server.URL, PathStyle: true, AccessKeyID: "AKID", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	testDisk(t, disk)
}

func TestS3VirtualHostAndPrefix(t *testing.T) {
	fake, server := newFakeS3(t, true)
	address := server.Listener.Addr().String()
	// Resolve media.s3.test to the fake server, as DNS would for a real bucket.
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}}}
	disk, err := NewS3(S3Config{Bucket: "media", Region: "eu-central-1", Endpoint: "http://s3.test", Prefix: "/tenant-1/", AccessKeyID: "AKID", SecretAccessKey: "secret", HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	testDisk(t, disk)
	ctx := context.Background()
	disk.Put(ctx, "a b/ç.txt", strings.NewReader("x"), PutOptions{})
	fake.mu.Lock()
	_, stored := fake.objects["tenant-1/a b/ç.txt"]
	fake.mu.Unlock()
	if !stored {
		t.Errorf("objects = %v", fake.objects)
	}
	if list, _ := disk.List(ctx, "a b"); len(list) != 1 || list[0].Path != "a b/ç.txt" {
		t.Errorf("List strips the prefix: %v", list)
	}
	if info, _ := disk.Stat(ctx, "a b/ç.txt"); info.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("content type = %q", info.ContentType)
	}

	signed, err := disk.TemporaryURL(ctx, "a b/ç.txt", time.Minute)
	if err != nil || !strings.HasPrefix(signed, "http://media.s3.test/tenant-1/a%20b/%C3%A7.txt?X-Amz-Algorithm=AWS4-HMAC-SHA256") {
		t.Fatalf("TemporaryURL = %q, %v", signed, err)
	}
	resp, err := client.Get(signed)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "x" {
		t.Errorf("presigned GET = %d %q", resp.StatusCode, body)
	}
	tampered := strings.Replace(signed, "%C3%A7.txt", "other.txt", 1)
	if resp, _ := client.Get(tampered); resp.StatusCode != http.StatusForbidden {
		t.Errorf("tampered presigned URL = %d", resp.StatusCode)
	}
	if fake.pages < 2 {
		t.Errorf("List did not paginate: %d pages", fake.pages)
	}
}

func TestS3URLsAndErrors(t *testing.T) {
	private, _ := NewS3(S3Config{Bucket: "b", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "s"})
	if _, err := private.URL("x.png"); !errors.Is(err, ErrPrivate) {
		t.Errorf("private URL = %v", err)
	}
	public, _ := NewS3(S3Config{Bucket: "b", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "s", Public: true, Prefix: "p"})
	if u, _ := public.URL("x y.png"); u != "https://b.s3.us-east-1.amazonaws.com/p/x%20y.png" {
		t.Errorf("public URL = %q", u)
	}
	cdn, _ := NewS3(S3Config{Bucket: "b", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "s", Public: true, PublicURL: "https://cdn.example.com/"})
	if u, _ := cdn.URL("img/x.png"); u != "https://cdn.example.com/img/x.png" {
		t.Errorf("CDN URL = %q", u)
	}
	for _, config := range []S3Config{
		{Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "s"},
		{Bucket: "b", Region: "us-east-1"},
		{Bucket: "b", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "s", Endpoint: "ftp://x"},
		{Bucket: "b", Region: "us-east-1", AccessKeyID: "a", SecretAccessKey: "s", Prefix: "../x"},
	} {
		if _, err := NewS3(config); err == nil {
			t.Errorf("NewS3(%+v) succeeded", config)
		}
	}

	_, server := newFakeS3(t, false)
	wrong, _ := NewS3(S3Config{Bucket: "media", Region: "eu-central-1", Endpoint: server.URL, PathStyle: true, AccessKeyID: "AKID", SecretAccessKey: "wrong"})
	err := wrong.Put(context.Background(), "x.txt", strings.NewReader("x"), PutOptions{})
	var s3err *S3Error
	if !errors.As(err, &s3err) || s3err.Status != 403 || s3err.Code != "SignatureDoesNotMatch" || errors.Is(err, ErrNotExist) {
		t.Errorf("wrong secret = %v", err)
	}
}

func TestSizedSpoolsUnknownReaders(t *testing.T) {
	file, _ := os.CreateTemp(t.TempDir(), "x")
	file.WriteString("0123456789")
	file.Seek(4, io.SeekStart)
	if r, n, cleanup, err := sized(file, 0); err != nil || n != 6 || r != io.Reader(file) {
		t.Errorf("seekable = %d, %v", n, err)
	} else {
		cleanup()
	}
	pr, pw := io.Pipe()
	go func() { pw.Write([]byte("streamed")); pw.Close() }()
	r, n, cleanup, err := sized(pr, 0)
	if err != nil || n != 8 {
		t.Fatalf("pipe = %d, %v", n, err)
	}
	spooled := r.(*os.File).Name()
	body, _ := io.ReadAll(r)
	cleanup()
	if string(body) != "streamed" {
		t.Errorf("spooled = %q", body)
	}
	if _, err := os.Stat(spooled); !os.IsNotExist(err) {
		t.Error("spool file left behind")
	}
}

// TestS3Integration runs the disk tests against a real S3-compatible service
// when GOLAR_TEST_S3_ENDPOINT is set, such as MinIO in CI:
//
//	GOLAR_TEST_S3_ENDPOINT=http://localhost:9000 GOLAR_TEST_S3_BUCKET=golar-test
//	GOLAR_TEST_S3_KEY=minioadmin GOLAR_TEST_S3_SECRET=minioadmin
func TestS3Integration(t *testing.T) {
	endpoint := os.Getenv("GOLAR_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("set GOLAR_TEST_S3_ENDPOINT to run against a real S3 service")
	}
	disk, err := NewS3(S3Config{
		Endpoint: endpoint, Bucket: os.Getenv("GOLAR_TEST_S3_BUCKET"), Region: "us-east-1", PathStyle: true,
		AccessKeyID: os.Getenv("GOLAR_TEST_S3_KEY"), SecretAccessKey: os.Getenv("GOLAR_TEST_S3_SECRET"),
		Prefix: "golar-test-" + strconv.FormatInt(time.Now().UnixNano(), 36),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Create the bucket; an existing one answers 409, which is fine.
	if resp, err := disk.do(context.Background(), http.MethodPut, disk.bucketURL(""), nil, 0, nil); err == nil {
		resp.Body.Close()
	} else if s3err := (*S3Error)(nil); !errors.As(err, &s3err) || s3err.Status != http.StatusConflict {
		t.Fatalf("create bucket: %v", err)
	}
	t.Cleanup(func() {
		files, _ := disk.List(context.Background(), "")
		for _, f := range files {
			disk.Delete(context.Background(), f.Path)
		}
	})
	testDisk(t, disk)
	ctx := context.Background()
	disk.Put(ctx, "signed/ü file.txt", strings.NewReader("presigned"), PutOptions{})
	signed, err := disk.TemporaryURL(ctx, "signed/ü file.txt", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(signed)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "presigned" {
		t.Errorf("presigned GET = %d %q", resp.StatusCode, body)
	}
}
