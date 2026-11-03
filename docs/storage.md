# File storage

`github.com/dayanchm/golar/storage` stores files on disks with one interface.
`Local` keeps them in a directory; `S3` in Amazon S3 or an S3-compatible
service such as MinIO, Cloudflare R2 or DigitalOcean Spaces. Neither adds a
dependency: the S3 driver signs requests with its own Signature Version 4
implementation.

```go
disk, err := storage.NewLocal(storage.LocalConfig{
    Root: "storage/app",
    URL:  "/files",
    Key:  []byte(os.Getenv("APP_KEY")), // at least 32 bytes; signs temporary URLs
})

err = disk.Put(ctx, "reports/2026.pdf", file, storage.PutOptions{ContentType: "application/pdf"})
r, err := disk.Get(ctx, "reports/2026.pdf") // stream; close it
info, err := disk.Stat(ctx, "reports/2026.pdf")
files, err := disk.List(ctx, "reports")
err = disk.Delete(ctx, "reports/2026.pdf")
link, err := disk.TemporaryURL(ctx, "reports/2026.pdf", 15*time.Minute)
```

## The Disk interface

| Method | Behaviour |
| --- | --- |
| `Put(ctx, path, r, opts)` | streams `r` to `path`, replacing any file; `opts.ContentType` defaults from the extension |
| `Get(ctx, path)` | opens the file for streaming |
| `Stat(ctx, path)` | size, modification time and content type |
| `Delete(ctx, path)` | removes the file; a missing file is not an error |
| `List(ctx, dir)` | files under a directory, recursively, sorted; `"reports"` does not match `reports-old/` |
| `URL(path)` | permanent URL on public disks; `ErrPrivate` otherwise |
| `TemporaryURL(ctx, path, d)` | a URL valid for `d`, between 1 second and 7 days |

Missing files return errors that match `errors.Is(err, storage.ErrNotExist)`.
Paths are slash-separated and relative. `storage.Clean` rejects empty and
absolute paths and those containing `..`, backslashes or NUL with
`ErrInvalidPath`, so a path built from user input cannot leave the disk.
Still, prefer `PutUpload`, which never uses user input in the path.

## Public and private disks

Visibility is per disk, as in Laravel: create one disk for public files and
another for private ones.

- **Public** disks (`Public: true`) return permanent URLs from `URL`.
- **Private** disks return `ErrPrivate` from `URL`. Their files are reachable
  only through `TemporaryURL`, which expires.

## Local disks

```go
public, _ := storage.NewLocal(storage.LocalConfig{Root: "storage/public", URL: "/media", Public: true})
private, _ := storage.NewLocal(storage.LocalConfig{Root: "storage/private", URL: "/files", Key: key})

mux.Handle("/media/", http.StripPrefix("/media", public.Handler()))
mux.Handle("/files/", http.StripPrefix("/files", private.Handler()))
```

All file access goes through Go's `os.Root`, so neither `..` nor a symbolic link
inside the directory can reach a file outside it. `Put` writes to a temporary
file and renames it, so readers never see a half-written file and concurrent
writers do not mix content.

`Handler` serves `GET` and `HEAD` with `Range` and conditional requests. On
private disks it requires a valid, unexpired signature (HMAC-SHA256 over the
path and expiry time, keyed by `Key`) and answers anything else with 403, with
`Cache-Control: private, no-store`. Every file is sent with
`X-Content-Type-Options: nosniff` and
`Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox`,
so an uploaded HTML or SVG file cannot run scripts on your site's origin.
Mount the handler at the path of `URL`, as above, so the URLs it signs match.

## S3 and compatible services

```go
disk, err := storage.NewS3(storage.S3Config{
    Bucket:          "my-app",
    Region:          "eu-central-1",
    AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
    SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
    Prefix:          "uploads", // optional key prefix
})
```

| Service | Settings |
| --- | --- |
| Amazon S3 | `Region`; the endpoint defaults to `https://s3.<region>.amazonaws.com` |
| MinIO | `Endpoint: "http://localhost:9000"`, `PathStyle: true`, `Region: "us-east-1"` |
| Cloudflare R2 | `Endpoint: "https://<account>.r2.cloudflarestorage.com"`, `Region: "auto"` |
| DigitalOcean Spaces | `Endpoint: "https://<region>.digitaloceanspaces.com"`, `Region: "<region>"` |

`SessionToken` supports temporary credentials. `Put` streams readers of known
length (`PutOptions.Size`, or files, `bytes.Reader` and `strings.Reader`)
straight to S3; other readers are spooled to a temporary file, never to memory.
A single upload is limited to 5 GiB (`storage.MaxS3Object`). `Get` streams
the object body. `List` follows S3 pagination. `TemporaryURL` returns a
presigned `GET` URL without contacting S3.

For public disks, `URL` returns `PublicURL` (such as a CDN) or the bucket URL.
Golar does not change ACLs; allow public reads with a bucket policy. Failed
requests return `*storage.S3Error` with the HTTP status and S3 error code.

## Uploads

Validate uploads with the file rules, then store them with `PutUpload`:

```go
type AvatarRequest struct {
    Avatar *multipart.FileHeader `form:"avatar" validate:"required,image,max_size=2MB"`
}

app.Post("/avatar", func(c *golar.Context) error {
    var in AvatarRequest
    if err := c.Validate(&in); err != nil {
        return err // 422 for a missing, oversized or non-image file
    }
    name, err := storage.PutUpload(c.Context(), disk, "avatars", in.Avatar)
    // name is like "avatars/3f2c5e…9a.png"
})
```

| Rule | Passes when |
| --- | --- |
| `file` | the field holds an uploaded file |
| `image` | the content is PNG, JPEG, GIF, WebP or BMP; SVG is excluded because it can carry scripts |
| `mimetypes=application/pdf image/*` | the content's type is one of the values; `type/*` matches a family |
| `extensions=jpg png` | the client's file name ends with one of the extensions |
| `max_size=2MB`, `min_size=1KB` | the size is within the limit; units are B, KB, MB and GB (powers of 1024) |

`image` and `mimetypes` detect the type from the first 512 bytes
(`validation.SniffContentType`) and ignore the client's `Content-Type` and
file name, which the client controls. `extensions` reads the file name, so
combine it with a content rule. Use `each` for several files:
`validate:"max=5,each,image"`.

`PutUpload` stores the file under a random 128-bit name with an extension
derived from the sniffed type (no extension for unknown types). The client's
file name never reaches the path.

## Streaming

`storage.Stream(w, r, disk, path)` copies a stored file to a response without
buffering it, with its type and length and the same `nosniff` and sandbox
headers as `Handler`. For local disks, `Handler` also supports `Range`. For
private S3 files, redirecting to `TemporaryURL` lets clients download from S3
directly instead of through your server.

## Testing

`storage.NewLocal` with `t.TempDir()` gives tests a real disk with no setup.
The S3 driver's tests run against an in-memory S3 that checks every request's
signature, and `storage.yml` in CI runs them against MinIO. To run them
locally against MinIO:

```sh
GOLAR_TEST_S3_ENDPOINT=http://localhost:9000 GOLAR_TEST_S3_BUCKET=golar-test \
GOLAR_TEST_S3_KEY=minioadmin GOLAR_TEST_S3_SECRET=minioadmin go test ./storage
```
