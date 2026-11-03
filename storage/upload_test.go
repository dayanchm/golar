package storage

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func formFile(t *testing.T, name string, body []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", name)
	part.Write(body)
	w.Close()
	form, err := multipart.NewReader(&buf, w.Boundary()).ReadForm(1 << 20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { form.RemoveAll() })
	return form.File["file"][0]
}

func TestPutUpload(t *testing.T) {
	disk, _ := NewLocal(LocalConfig{Root: t.TempDir()})
	ctx := context.Background()
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 50)...)
	name, err := PutUpload(ctx, disk, "avatars", formFile(t, "../../etc/passwd.php", png))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^avatars/[0-9a-f]{32}\.png$`).MatchString(name) {
		t.Errorf("stored as %q", name)
	}
	if info, err := disk.Stat(ctx, name); err != nil || info.Size != int64(len(png)) {
		t.Errorf("Stat = %+v, %v", info, err)
	}
	other, _ := PutUpload(ctx, disk, "", formFile(t, "notes.exe", []byte("plain notes")))
	if !strings.HasSuffix(other, ".txt") || strings.Contains(other, "/") {
		t.Errorf("text upload stored as %q", other)
	}
	unknown, _ := PutUpload(ctx, disk, "x", formFile(t, "a.bin", []byte{0x00, 0x01, 0xfe}))
	if !regexp.MustCompile(`^x/[0-9a-f]{32}$`).MatchString(unknown) {
		t.Errorf("unknown type stored as %q", unknown)
	}
	if _, err := PutUpload(ctx, disk, "../up", formFile(t, "a.png", png)); err == nil {
		t.Error("invalid dir accepted")
	}

	rec := httptest.NewRecorder()
	if err := Stream(rec, httptest.NewRequest("GET", "/", nil), disk, name); err != nil {
		t.Fatal(err)
	}
	if rec.Body.Len() != len(png) || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("Content-Length") != "58" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Last-Modified") == "" {
		t.Errorf("Stream = %d bytes, %v", rec.Body.Len(), rec.Header())
	}
	head := httptest.NewRecorder()
	Stream(head, httptest.NewRequest("HEAD", "/", nil), disk, name)
	if head.Body.Len() != 0 || head.Header().Get("Content-Length") != "58" {
		t.Errorf("HEAD = %d bytes, %v", head.Body.Len(), head.Header())
	}
	missing := httptest.NewRecorder()
	if err := Stream(missing, httptest.NewRequest("GET", "/", nil), disk, "nope.png"); err == nil || missing.Code != 404 {
		t.Errorf("missing = %d, %v", missing.Code, err)
	}
}
