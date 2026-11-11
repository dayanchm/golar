package storage

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
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
	// uploads holds unfinished multipart uploads by ID; failPart makes that
	// part number fail.
	uploads  map[string]*fakeUpload
	nextID   int
	failPart int
}

type fakeUpload struct {
	key, contentType string
	parts            map[int][]byte
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
	query := r.URL.Query()
	switch {
	case r.Method == http.MethodPost && query.Has("uploads"):
		f.nextID++
		id := "upload-" + strconv.Itoa(f.nextID)
		f.uploads[id] = &fakeUpload{key: key, contentType: r.Header.Get("Content-Type"), parts: map[int][]byte{}}
		fmt.Fprintf(w, `<InitiateMultipartUploadResult><Bucket>%s</Bucket><Key>%s</Key><UploadId>%s</UploadId></InitiateMultipartUploadResult>`, f.bucket, key, id)
	case query.Has("uploadId"):
		upload, ok := f.uploads[query.Get("uploadId")]
		if !ok || upload.key != key {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		switch r.Method {
		case http.MethodPut:
			number, _ := strconv.Atoi(query.Get("partNumber"))
			body, _ := io.ReadAll(r.Body)
			if number < 1 || int64(len(body)) != r.ContentLength || number == f.failPart {
				w.WriteHeader(http.StatusInternalServerError)
				io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
				return
			}
			upload.parts[number] = body
			w.Header().Set("ETag", strconv.Quote("etag-"+strconv.Itoa(number)))
		case http.MethodPost:
			var complete struct {
				Parts []struct {
					PartNumber int
					ETag       string
				} `xml:"Part"`
			}
			if err := xml.NewDecoder(r.Body).Decode(&complete); err != nil || len(complete.Parts) != len(upload.parts) {
				// Real S3 reports this with 200 and an Error document.
				io.WriteString(w, `<Error><Code>InvalidPart</Code><Message>parts do not match</Message></Error>`)
				return
			}
			var body []byte
			for i, part := range complete.Parts {
				if part.PartNumber != i+1 || part.ETag != strconv.Quote("etag-"+strconv.Itoa(i+1)) {
					io.WriteString(w, `<Error><Code>InvalidPart</Code></Error>`)
					return
				}
				body = append(body, upload.parts[part.PartNumber]...)
			}
			f.objects[key] = fakeObject{body, upload.contentType, time.Now().UTC().Truncate(time.Second)}
			delete(f.uploads, query.Get("uploadId"))
			io.WriteString(w, `<CompleteMultipartUploadResult><Key>`+key+`</Key></CompleteMultipartUploadResult>`)
		case http.MethodDelete:
			delete(f.uploads, query.Get("uploadId"))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
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
	f := &fakeS3{t: t, creds: sigCredentials{accessKey: "AKID", secretKey: "secret", region: "eu-central-1"}, bucket: "media", virtual: virtual, objects: map[string]fakeObject{}, uploads: map[string]*fakeUpload{}}
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

func TestKnownSize(t *testing.T) {
	file, _ := os.CreateTemp(t.TempDir(), "x")
	file.WriteString("0123456789")
	file.Seek(4, io.SeekStart)
	if n, ok := knownSize(file, 0); !ok || n != 6 {
		t.Errorf("seekable = %d, %v", n, ok)
	}
	if n, ok := knownSize(strings.NewReader("abc"), 0); !ok || n != 3 {
		t.Errorf("strings.Reader = %d, %v", n, ok)
	}
	if n, ok := knownSize(io.LimitReader(strings.NewReader("abc"), 9), 7); !ok || n != 7 {
		t.Errorf("explicit size = %d, %v", n, ok)
	}
	pr, _ := io.Pipe()
	if _, ok := knownSize(pr, 0); ok {
		t.Error("pipe reported a size")
	}
}

// onlyReader hides every interface but io.Reader, as a network body does.
type onlyReader struct{ io.Reader }

// failingReader returns its data, then err.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

func TestS3Multipart(t *testing.T) {
	fake, server := newFakeS3(t, false)
	disk, err := NewS3(S3Config{Bucket: "media", Region: "eu-central-1", Endpoint: server.URL, PathStyle: true, AccessKeyID: "AKID", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	disk.partSize = 1000 // below S3's minimum, to keep the test small
	ctx := context.Background()
	data := make([]byte, 3500)
	for i := range data {
		data[i] = byte(i % 251)
	}
	stored := func(key string) []byte {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		return fake.objects[key].body
	}
	cases := []struct {
		name string
		body io.Reader
		opts PutOptions
		size int
	}{
		{"known.bin", bytes.NewReader(data), PutOptions{}, 3500},
		{"unknown.bin", onlyReader{bytes.NewReader(data)}, PutOptions{}, 3500},
		{"exact.bin", onlyReader{bytes.NewReader(data[:3000])}, PutOptions{}, 3000},
		{"sized.bin", onlyReader{bytes.NewReader(data[:2001])}, PutOptions{Size: 2001}, 2001},
		{"single.bin", onlyReader{bytes.NewReader(data[:1000])}, PutOptions{}, 1000},
		{"empty.bin", onlyReader{strings.NewReader("")}, PutOptions{}, 0},
	}
	for _, c := range cases {
		if err := disk.Put(ctx, c.name, c.body, c.opts); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := stored(c.name); !bytes.Equal(got, data[:c.size]) {
			t.Errorf("%s: stored %d bytes, want %d", c.name, len(got), c.size)
		}
	}
	if info, _ := disk.Stat(ctx, "known.bin"); info.ContentType != "application/octet-stream" {
		t.Errorf("content type = %q", info.ContentType)
	}

	failures := []struct {
		name string
		put  func() error
	}{
		{"part fails", func() error {
			fake.mu.Lock()
			fake.failPart = 2
			fake.mu.Unlock()
			defer func() { fake.mu.Lock(); fake.failPart = 0; fake.mu.Unlock() }()
			return disk.Put(ctx, "failed.bin", bytes.NewReader(data), PutOptions{})
		}},
		{"reader fails", func() error {
			return disk.Put(ctx, "failed.bin", &failingReader{data[:1500], errors.New("connection reset")}, PutOptions{})
		}},
		{"reader is short", func() error {
			return disk.Put(ctx, "failed.bin", onlyReader{bytes.NewReader(data[:1500])}, PutOptions{Size: 3500})
		}},
		{"context cancelled", func() error {
			ctx, cancel := context.WithCancel(ctx)
			r := &failingReader{data[:1500], nil}
			r.err = errors.New("unreachable")
			reader := io.MultiReader(r, readerFunc(func([]byte) (int, error) { cancel(); return 0, ctx.Err() }))
			return disk.Put(ctx, "failed.bin", reader, PutOptions{})
		}},
	}
	for _, f := range failures {
		if err := f.put(); err == nil {
			t.Errorf("%s: Put succeeded", f.name)
		}
		fake.mu.Lock()
		left, object := len(fake.uploads), fake.objects["failed.bin"].body
		fake.mu.Unlock()
		if left != 0 || object != nil {
			t.Errorf("%s: %d unfinished uploads and object %v left behind", f.name, left, object != nil)
		}
	}

	if _, err := NewS3(S3Config{Bucket: "b", Region: "r", AccessKeyID: "a", SecretAccessKey: "s", PartSize: 1 << 20}); err == nil {
		t.Error("NewS3 accepted a 1 MiB part size")
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// TestS3Integration runs the disk tests against a real S3-compatible service
// when GOLAR_TEST_S3_ENDPOINT is set, such as Versity Gateway in CI or MinIO:
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

	// Multipart: 11 MiB of unknown length in 5 MiB parts, the S3 minimum.
	disk.partSize = minS3PartSize
	large := make([]byte, 11<<20)
	for i := range large {
		large[i] = byte(i % 251)
	}
	if err := disk.Put(ctx, "large.bin", onlyReader{bytes.NewReader(large)}, PutOptions{}); err != nil {
		t.Fatalf("multipart Put: %v", err)
	}
	reader, err := disk.Get(ctx, "large.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || !bytes.Equal(got, large) {
		t.Errorf("multipart Get = %d bytes, %v", len(got), err)
	}
	failing := &failingReader{large[:6<<20], errors.New("connection reset")}
	if err := disk.Put(ctx, "failed.bin", failing, PutOptions{}); err == nil {
		t.Error("failed multipart Put succeeded")
	}
	if uploads := unfinishedUploads(t, disk); len(uploads) != 0 {
		t.Errorf("unfinished uploads left behind: %v", uploads)
	}
	if _, err := disk.Stat(ctx, "failed.bin"); !errors.Is(err, ErrNotExist) {
		t.Errorf("failed upload created an object: %v", err)
	}
}

// unfinishedUploads lists the multipart uploads in progress under the
// disk's prefix.
func unfinishedUploads(t *testing.T, disk *S3) []string {
	t.Helper()
	u := disk.bucketURL("")
	u.RawQuery = canonicalQuery(url.Values{"uploads": {""}, "prefix": {disk.config.Prefix + "/"}})
	resp, err := disk.do(context.Background(), http.MethodGet, u, nil, 0, nil)
	if err != nil {
		t.Fatalf("list multipart uploads: %v", err)
	}
	defer resp.Body.Close()
	var result struct {
		Uploads []struct {
			Key string `xml:"Key"`
		} `xml:"Upload"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("list multipart uploads: %v", err)
	}
	var keys []string
	for _, upload := range result.Uploads {
		keys = append(keys, upload.Key)
	}
	return keys
}
