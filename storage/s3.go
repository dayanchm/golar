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
	// HTTPClient defaults to a client with a 5 minute timeout per request;
	// a multipart upload makes one request per part.
	HTTPClient *http.Client
	// PartSize is the size of each part of a multipart upload, 16 MiB by
	// default and at least 5 MiB. Uploads up to PartSize use a single PUT;
	// larger ones, and those of unknown size, are uploaded in parts, with at
	// most one part held in memory. It grows for large uploads of known size
	// to stay within S3's 10,000 parts.
	PartSize int64
}

// S3 stores files in a bucket. Uploads and downloads are streamed.
type S3 struct {
	config   S3Config
	endpoint *url.URL
	creds    sigCredentials
	client   *http.Client
	partSize int64
	now      func() time.Time
}

const (
	// MaxS3Object is the largest single PUT that S3 accepts. Larger files
	// are uploaded in parts.
	MaxS3Object = 5 << 30
	// MaxS3MultipartObject is the largest object S3 stores.
	MaxS3MultipartObject = 5 << 40

	minS3PartSize     = 5 << 20
	defaultS3PartSize = 16 << 20
	maxS3Parts        = 10000
)

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
	partSize := config.PartSize
	switch {
	case partSize == 0:
		partSize = defaultS3PartSize
	case partSize < minS3PartSize || partSize > MaxS3Object:
		return nil, errors.New("storage: S3Config.PartSize must be between 5 MiB and 5 GiB")
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
		partSize: partSize,
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

// knownSize returns the number of bytes left in r when it can tell without
// reading: from size, or from readers that report their length.
func knownSize(r io.Reader, size int64) (int64, bool) {
	if size > 0 {
		return size, true
	}
	switch v := r.(type) {
	case *bytes.Reader:
		return int64(v.Len()), true
	case *strings.Reader:
		return int64(v.Len()), true
	case *bytes.Buffer:
		return int64(v.Len()), true
	}
	if seeker, ok := r.(io.Seeker); ok {
		current, err := seeker.Seek(0, io.SeekCurrent)
		if err == nil {
			if end, err := seeker.Seek(0, io.SeekEnd); err == nil {
				if _, err := seeker.Seek(current, io.SeekStart); err == nil {
					return end - current, true
				}
			}
		}
	}
	return 0, false
}

func (s *S3) Put(ctx context.Context, p string, r io.Reader, opts PutOptions) error {
	name, err := Clean(p)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	contentType := opts.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(name))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	size, known := knownSize(r, opts.Size)
	if known && size > MaxS3MultipartObject {
		return fmt.Errorf("storage: %s is larger than the 5 TiB S3 object limit", name)
	}
	if known && size <= s.partSize {
		return s.putObject(ctx, name, r, size, contentType)
	}
	partSize := s.partSize
	if known {
		// Grow parts in whole MiB so the upload fits in maxS3Parts.
		const mib = 1 << 20
		if need := (size + maxS3Parts - 1) / maxS3Parts; need > partSize {
			partSize = (need + mib - 1) / mib * mib
		}
		return s.putMultipart(ctx, name, r, contentType, partSize, size)
	}
	// Unknown size: read one part ahead to choose between a single PUT and
	// a multipart upload.
	first := make([]byte, partSize)
	n, err := io.ReadFull(r, first)
	switch {
	case err == io.EOF || err == io.ErrUnexpectedEOF:
		return s.putObject(ctx, name, bytes.NewReader(first[:n]), int64(n), contentType)
	case err != nil:
		return err
	}
	return s.putMultipart(ctx, name, io.MultiReader(bytes.NewReader(first), r), contentType, partSize, -1)
}

func (s *S3) putObject(ctx context.Context, name string, body io.Reader, size int64, contentType string) error {
	// http.NoBody is passed as is: wrapped, a zero length would read as
	// unknown and the request would be chunked, which S3 rejects.
	if size == 0 {
		body = http.NoBody
	} else {
		body = contextReader{ctx, body}
	}
	resp, err := s.do(ctx, http.MethodPut, s.objectURL(name), body, size, http.Header{"Content-Type": {contentType}})
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// uploadURL returns the object's URL with the given query.
func (s *S3) uploadURL(name string, query url.Values) *url.URL {
	u := s.objectURL(name)
	u.RawQuery = canonicalQuery(query)
	return u
}

// putMultipart uploads r in parts of partSize. size is the total length, or
// -1 when unknown, in which case each part is buffered to learn its length.
// Any failure aborts the upload so no parts are left behind.
func (s *S3) putMultipart(ctx context.Context, name string, r io.Reader, contentType string, partSize, size int64) (err error) {
	resp, err := s.do(ctx, http.MethodPost, s.uploadURL(name, url.Values{"uploads": {""}}), nil, 0, http.Header{"Content-Type": {contentType}})
	if err != nil {
		return err
	}
	var initiated struct {
		UploadID string `xml:"UploadId"`
	}
	err = xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&initiated)
	resp.Body.Close()
	if err != nil || initiated.UploadID == "" {
		return fmt.Errorf("storage: S3 multipart upload of %s: no upload ID (%v)", name, err)
	}
	uploadID := initiated.UploadID
	defer func() {
		if err != nil {
			err = errors.Join(err, s.abortMultipart(ctx, name, uploadID))
		}
	}()

	type part struct {
		XMLName    xml.Name `xml:"Part"`
		PartNumber int
		ETag       string
	}
	var parts []part
	var buf []byte
	if size < 0 {
		buf = make([]byte, partSize)
	}
	for number := 1; ; number++ {
		var body io.Reader
		var length int64
		if size >= 0 {
			length = min(partSize, size)
			size -= length
			body = io.LimitReader(r, length)
		} else {
			n, readErr := io.ReadFull(r, buf)
			if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
				return readErr
			}
			length, body = int64(n), bytes.NewReader(buf[:n])
		}
		if length == 0 && number > 1 {
			break
		}
		if number > maxS3Parts {
			return fmt.Errorf("storage: %s needs more than %d parts; set S3Config.PartSize or PutOptions.Size", name, maxS3Parts)
		}
		query := url.Values{"partNumber": {strconv.Itoa(number)}, "uploadId": {uploadID}}
		resp, err := s.do(ctx, http.MethodPut, s.uploadURL(name, query), contextReader{ctx, body}, length, nil)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		parts = append(parts, part{PartNumber: number, ETag: resp.Header.Get("ETag")})
		if length < partSize || size == 0 {
			break
		}
	}

	complete, err := xml.Marshal(struct {
		XMLName xml.Name `xml:"CompleteMultipartUpload"`
		Parts   []part
	}{Parts: parts})
	if err != nil {
		return err
	}
	resp, err = s.do(ctx, http.MethodPost, s.uploadURL(name, url.Values{"uploadId": {uploadID}}), bytes.NewReader(complete), int64(len(complete)), http.Header{"Content-Type": {"application/xml"}})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// S3 can report a failed completion with 200 and an Error document.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var result struct {
		XMLName xml.Name
		Code    string
		Message string
	}
	if xml.Unmarshal(body, &result) == nil && result.XMLName.Local == "Error" {
		return &S3Error{Status: resp.StatusCode, Code: result.Code, Message: result.Message}
	}
	return nil
}

// abortMultipart removes an unfinished upload's parts. It runs even when ctx
// was cancelled, since a cancelled upload must be cleaned up too.
func (s *S3) abortMultipart(ctx context.Context, name, uploadID string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	resp, err := s.do(ctx, http.MethodDelete, s.uploadURL(name, url.Values{"uploadId": {uploadID}}), nil, 0, nil)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return nil
		}
		return fmt.Errorf("storage: abort multipart upload of %s: %w", name, err)
	}
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
