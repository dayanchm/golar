package storage

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// S3Config configures an S3 disk. It works with Amazon S3 and S3-compatible
// services such as MinIO, Cloudflare R2 and DigitalOcean Spaces.
type S3Config struct {
	Bucket string
	Region string // such as "us-east-1"; R2 uses "auto"
	// Endpoint defaults to https://s3.<Region>.amazonaws.com. Set it for
	// S3-compatible services, such as "http://localhost:9000" for MinIO.
	Endpoint                     string
	AccessKeyID, SecretAccessKey string
	SessionToken                 string // for temporary credentials
	// PathStyle addresses the bucket as endpoint/bucket instead of
	// bucket.endpoint, as MinIO and most self-hosted services need.
	PathStyle bool
	// Prefix is prepended to every key, such as "uploads".
	Prefix string
	// Public disks return permanent URLs from URL. Making objects readable
	// is up to the bucket policy; Golar does not change ACLs.
	Public bool
	// PublicURL is the base of permanent URLs, such as a CDN; it defaults to
	// the bucket's URL.
	PublicURL string
	// HTTPClient defaults to a client with a 5 minute timeout.
	HTTPClient *http.Client
}

// S3 stores files in a bucket. Uploads and downloads are streamed.
type S3 struct {
	config   S3Config
	endpoint *url.URL
	creds    sigCredentials
	client   *http.Client
	now      func() time.Time
}

// MaxS3Object is the largest single PUT that S3 accepts.
const MaxS3Object = 5 << 30

// NewS3 validates config. It does not contact the service.
func NewS3(config S3Config) (*S3, error) {
	if config.Bucket == "" || config.Region == "" {
		return nil, errors.New("storage: S3Config.Bucket and Region are required")
	}
	if config.AccessKeyID == "" || config.SecretAccessKey == "" {
		return nil, errors.New("storage: S3Config.AccessKeyID and SecretAccessKey are required")
	}
	if config.Endpoint == "" {
		config.Endpoint = "https://s3." + config.Region + ".amazonaws.com"
	}
	endpoint, err := url.Parse(strings.TrimRight(config.Endpoint, "/"))
	if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Host == "" {
		return nil, fmt.Errorf("storage: invalid S3 endpoint %q", config.Endpoint)
	}
	if config.Prefix != "" {
		prefix, err := Clean(strings.Trim(config.Prefix, "/"))
		if err != nil {
			return nil, fmt.Errorf("storage: S3Config.Prefix: %w", err)
		}
		config.Prefix = prefix
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	return &S3{
		config:   config,
		endpoint: endpoint,
		creds:    sigCredentials{accessKey: config.AccessKeyID, secretKey: config.SecretAccessKey, sessionToken: config.SessionToken, region: config.Region},
		client:   client,
		now:      time.Now,
	}, nil
}

func (s *S3) key(name string) string {
	if s.config.Prefix == "" {
		return name
	}
	return s.config.Prefix + "/" + name
}

// bucketURL returns the URL of the bucket root or of an escaped key in it.
func (s *S3) bucketURL(escapedKey string) *url.URL {
	u := *s.endpoint
	if s.config.PathStyle {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + s.config.Bucket + "/" + escapedKey
	} else {
		u.Host = s.config.Bucket + "." + u.Host
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + escapedKey
	}
	u.RawPath = ""
	if unescaped, err := url.PathUnescape(u.Path); err == nil {
		u.RawPath, u.Path = u.Path, unescaped
	}
	return &u
}

func (s *S3) objectURL(name string) *url.URL { return s.bucketURL(escapePath(s.key(name))) }

// S3Error is a failed S3 request.
type S3Error struct {
	Status  int
	Code    string
	Message string
}

func (e *S3Error) Error() string {
	return fmt.Sprintf("storage: S3 %d %s: %s", e.Status, e.Code, e.Message)
}

// Unwrap reports missing objects as ErrNotExist.
func (e *S3Error) Unwrap() error {
	if e.Status == http.StatusNotFound || e.Code == "NoSuchKey" {
		return ErrNotExist
	}
	return nil
}

func s3Error(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var parsed struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	_ = xml.Unmarshal(body, &parsed)
	if parsed.Code == "" {
		parsed.Code = http.StatusText(resp.StatusCode)
	}
	return &S3Error{Status: resp.StatusCode, Code: parsed.Code, Message: parsed.Message}
}

func (s *S3) do(ctx context.Context, method string, u *url.URL, body io.Reader, size int64, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	for name, values := range header {
		req.Header[name] = values
	}
	if body != nil {
		req.ContentLength = size
	}
	payload := emptySHA256
	if body != nil {
		payload = unsignedPayload
	}
	s.creds.sign(req, payload, s.now())
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage: S3 %s: %w", method, err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		if method == http.MethodHead {
			return nil, &S3Error{Status: resp.StatusCode, Code: http.StatusText(resp.StatusCode)}
		}
		return nil, s3Error(resp)
	}
	return resp, nil
}

// sized returns r with its length, spooling readers of unknown length to a
// temporary file so memory use stays flat. The caller runs cleanup.
func sized(r io.Reader, size int64) (io.Reader, int64, func(), error) {
	noop := func() {}
	if size > 0 {
		return r, size, noop, nil
	}
	switch v := r.(type) {
	case *bytes.Reader:
		return v, int64(v.Len()), noop, nil
	case *strings.Reader:
		return v, int64(v.Len()), noop, nil
	case *bytes.Buffer:
		return v, int64(v.Len()), noop, nil
	}
	if seeker, ok := r.(io.Seeker); ok {
		current, err := seeker.Seek(0, io.SeekCurrent)
		if err == nil {
			if end, err := seeker.Seek(0, io.SeekEnd); err == nil {
				if _, err := seeker.Seek(current, io.SeekStart); err == nil {
					return r, end - current, noop, nil
				}
			}
		}
	}
	tmp, err := os.CreateTemp("", "golar-s3-*")
	if err != nil {
		return nil, 0, noop, err
	}
	cleanup := func() { tmp.Close(); os.Remove(tmp.Name()) }
	n, err := io.Copy(tmp, io.LimitReader(r, MaxS3Object+1))
	if err == nil {
		_, err = tmp.Seek(0, io.SeekStart)
	}
	if err != nil {
		cleanup()
		return nil, 0, noop, err
	}
	return tmp, n, cleanup, nil
}

func (s *S3) Put(ctx context.Context, p string, r io.Reader, opts PutOptions) error {
	name, err := Clean(p)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	body, size, cleanup, err := sized(r, opts.Size)
	if err != nil {
		return err
	}
	defer cleanup()
	if size > MaxS3Object {
		return fmt.Errorf("storage: %s is larger than the 5 GiB S3 single-upload limit", name)
	}
	contentType := opts.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(name))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if size == 0 {
		body = http.NoBody
	}
	resp, err := s.do(ctx, http.MethodPut, s.objectURL(name), contextReader{ctx, body}, size, http.Header{"Content-Type": {contentType}})
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

func (s *S3) Get(ctx context.Context, p string) (io.ReadCloser, error) {
	name, err := Clean(p)
	if err != nil {
		return nil, err
	}
	resp, err := s.do(ctx, http.MethodGet, s.objectURL(name), nil, 0, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

func (s *S3) Stat(ctx context.Context, p string) (FileInfo, error) {
	name, err := Clean(p)
	if err != nil {
		return FileInfo{}, err
	}
	resp, err := s.do(ctx, http.MethodHead, s.objectURL(name), nil, 0, nil)
	if err != nil {
		return FileInfo{}, err
	}
	resp.Body.Close()
	size, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	modified, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	return FileInfo{Path: name, Size: size, ModTime: modified, ContentType: resp.Header.Get("Content-Type")}, nil
}

func (s *S3) Delete(ctx context.Context, p string) error {
	name, err := Clean(p)
	if err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodDelete, s.objectURL(name), nil, 0, nil)
	if errors.Is(err, ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

func (s *S3) List(ctx context.Context, prefix string) ([]FileInfo, error) {
	dir, err := cleanPrefix(prefix)
	if err != nil {
		return nil, err
	}
	keyPrefix := s.key(dir)
	if dir == "" {
		keyPrefix = s.config.Prefix
	}
	if keyPrefix != "" {
		keyPrefix += "/"
	}
	var files []FileInfo
	token := ""
	for {
		u := s.bucketURL("")
		query := url.Values{"list-type": {"2"}}
		if keyPrefix != "" {
			query.Set("prefix", keyPrefix)
		}
		if token != "" {
			query.Set("continuation-token", token)
		}
		u.RawQuery = canonicalQuery(query)
		resp, err := s.do(ctx, http.MethodGet, u, nil, 0, nil)
		if err != nil {
			return nil, err
		}
		var page struct {
			Contents []struct {
				Key          string `xml:"Key"`
				Size         int64  `xml:"Size"`
				LastModified string `xml:"LastModified"`
			} `xml:"Contents"`
			IsTruncated           bool   `xml:"IsTruncated"`
			NextContinuationToken string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(&page)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("storage: S3 list: %w", err)
		}
		for _, object := range page.Contents {
			name := object.Key
			if s.config.Prefix != "" {
				name = strings.TrimPrefix(name, s.config.Prefix+"/")
			}
			if strings.HasSuffix(name, "/") {
				continue // folder placeholder objects
			}
			modified, _ := time.Parse(time.RFC3339, object.LastModified)
			files = append(files, FileInfo{Path: name, Size: object.Size, ModTime: modified})
		}
		if !page.IsTruncated || page.NextContinuationToken == "" {
			return files, nil
		}
		token = page.NextContinuationToken
	}
}

func (s *S3) URL(p string) (string, error) {
	if !s.config.Public {
		return "", ErrPrivate
	}
	name, err := Clean(p)
	if err != nil {
		return "", err
	}
	if s.config.PublicURL != "" {
		return strings.TrimRight(s.config.PublicURL, "/") + "/" + escapePath(s.key(name)), nil
	}
	return s.objectURL(name).String(), nil
}

// TemporaryURL returns a presigned GET URL; it needs no request to S3.
func (s *S3) TemporaryURL(_ context.Context, p string, expires time.Duration) (string, error) {
	name, err := Clean(p)
	if err != nil {
		return "", err
	}
	if err := checkExpiry(expires); err != nil {
		return "", err
	}
	return s.creds.presign(s.objectURL(name), expires, s.now()), nil
}
