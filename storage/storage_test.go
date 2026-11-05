package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var testKey = []byte(strings.Repeat("k", 32))

func TestClean(t *testing.T) {
	for in, want := range map[string]string{"a.txt": "a.txt", "a/b/c.png": "a/b/c.png", "a//b": "a/b", "a/./b": "a/b", "ü/ş.txt": "ü/ş.txt"} {
		if got, err := Clean(in); err != nil || got != want {
			t.Errorf("Clean(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", ".", "/etc/passwd", "../x", "a/../../x", "a/..", `a\b`, "a\x00b", "./"} {
		if _, err := Clean(bad); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Clean(%q) error = %v", bad, err)
		}
	}
}

// testDisk is the behaviour every Disk must have.
func testDisk(t *testing.T, disk Disk) {
	ctx := context.Background()
	read := func(p string) string {
		t.Helper()
		r, err := disk.Get(ctx, p)
		if err != nil {
			t.Fatalf("Get(%s): %v", p, err)
		}
		defer r.Close()
		body, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	if err := disk.Put(ctx, "docs/a.txt", strings.NewReader("hello"), PutOptions{ContentType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if got := read("docs/a.txt"); got != "hello" {
		t.Errorf("read = %q", got)
	}
	// Unknown sizes are streamed; replacing keeps one file.
	if err := disk.Put(ctx, "docs/a.txt", io.MultiReader(strings.NewReader("hello "), strings.NewReader("again")), PutOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := read("docs/a.txt"); got != "hello again" {
		t.Errorf("replaced = %q", got)
	}
	big := bytes.Repeat([]byte("0123456789"), 300_000) // 3 MB
	if err := disk.Put(ctx, "docs/nested/big.bin", bytes.NewReader(big), PutOptions{Size: int64(len(big))}); err != nil {
		t.Fatal(err)
	}
	if err := disk.Put(ctx, "other/ü ş.txt", strings.NewReader("unicode"), PutOptions{}); err != nil {
		t.Fatal(err)
	}
	info, err := disk.Stat(ctx, "docs/nested/big.bin")
	if err != nil || info.Size != int64(len(big)) || info.Path != "docs/nested/big.bin" || info.ModTime.IsZero() {
		t.Errorf("Stat = %+v, %v", info, err)
	}
	if read("docs/nested/big.bin") != string(big) {
		t.Error("big file changed")
	}
	if read("other/ü ş.txt") != "unicode" {
		t.Error("unicode name changed")
	}
	if _, err := disk.Stat(ctx, "docs/missing.txt"); !errors.Is(err, ErrNotExist) {
		t.Errorf("Stat(missing) = %v", err)
	}
	if _, err := disk.Get(ctx, "docs/missing.txt"); !errors.Is(err, ErrNotExist) {
		t.Errorf("Get(missing) = %v", err)
	}

	list, err := disk.List(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range list {
		names = append(names, f.Path)
	}
	if strings.Join(names, ",") != "docs/a.txt,docs/nested/big.bin" {
		t.Errorf("List(docs) = %v", names)
	}
	if all, err := disk.List(ctx, ""); err != nil || len(all) != 3 {
		t.Errorf("List() = %v, %v", all, err)
	}
	if none, err := disk.List(ctx, "nothing/here"); err != nil || len(none) != 0 {
		t.Errorf("List(missing) = %v, %v", none, err)
	}
	if doc, err := disk.List(ctx, "doc"); err != nil || len(doc) != 0 {
		t.Errorf("List(doc) matched a partial directory name: %v, %v", doc, err)
	}

	if err := disk.Delete(ctx, "docs/a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := disk.Delete(ctx, "docs/a.txt"); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	if _, err := disk.Stat(ctx, "docs/a.txt"); !errors.Is(err, ErrNotExist) {
		t.Errorf("deleted file still exists: %v", err)
	}

	for _, bad := range []string{"../escape.txt", "/abs.txt", ""} {
		if err := disk.Put(ctx, bad, strings.NewReader("x"), PutOptions{}); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Put(%q) = %v", bad, err)
		}
		if _, err := disk.Get(ctx, bad); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Get(%q) = %v", bad, err)
		}
	}
	if _, err := disk.TemporaryURL(ctx, "docs/nested/big.bin", 8*24*time.Hour); err == nil {
		t.Error("TemporaryURL accepted more than 7 days")
	}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			body := strings.Repeat(string(rune('a'+i)), 1000)
			if err := disk.Put(ctx, "race.txt", strings.NewReader(body), PutOptions{Size: 1000}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := read("race.txt"); len(got) != 1000 || strings.Count(got, got[:1]) != 1000 {
		t.Errorf("concurrent writes mixed content: %q...", got[:20])
	}
	disk.Delete(ctx, "race.txt")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := disk.Put(cancelled, "cancelled.txt", strings.NewReader("x"), PutOptions{}); err == nil {
		t.Error("Put ignored a cancelled context")
	}
	if _, err := disk.Stat(ctx, "cancelled.txt"); !errors.Is(err, ErrNotExist) {
		t.Errorf("cancelled upload left a file: %v", err)
	}
}

func TestLocalDisk(t *testing.T) {
	disk, err := NewLocal(LocalConfig{Root: t.TempDir(), URL: "/files", Key: testKey})
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	testDisk(t, disk)
}

func TestLocalStaysInsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	secret := filepath.Join(parent, "secret.txt")
	os.WriteFile(secret, []byte("secret"), 0o600)
	disk, err := NewLocal(LocalConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// A symbolic link inside the root that points outside it must not be followed.
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	os.Symlink(parent, filepath.Join(root, "linkdir"))
	if _, err := disk.Get(ctx, "link.txt"); err == nil {
		t.Error("read a file outside the root through a symlink")
	}
	if err := disk.Put(ctx, "linkdir/planted.txt", strings.NewReader("x"), PutOptions{}); err == nil {
		t.Error("wrote outside the root through a symlinked directory")
	}
	if _, err := os.Stat(filepath.Join(parent, "planted.txt")); err == nil {
		t.Error("file planted outside the root")
	}
	if err := disk.Delete(ctx, "link.txt"); err != nil {
		t.Errorf("deleting the link itself: %v", err)
	}
	if _, err := os.Stat(secret); err != nil {
		t.Error("deleting a link removed its target")
	}
	if err := disk.Put(ctx, ".golar-upload-x", strings.NewReader("x"), PutOptions{}); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("reserved temp name accepted: %v", err)
	}
	os.Mkdir(filepath.Join(root, "dir"), 0o755)
	if err := disk.Delete(ctx, "dir"); err == nil {
		t.Error("Delete removed a directory")
	}
	if _, err := disk.Stat(ctx, "dir"); !errors.Is(err, ErrNotExist) {
		t.Errorf("Stat(dir) = %v", err)
	}
	if _, err := NewLocal(LocalConfig{Root: root, Key: []byte("short")}); err == nil {
		t.Error("short key accepted")
	}
	if _, err := NewLocal(LocalConfig{}); err == nil {
		t.Error("empty root accepted")
	}
}

func serve(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestLocalURLsAndHandler(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	private, _ := NewLocal(LocalConfig{Root: dir, URL: "https://app.example.com/files/", Key: testKey})
	public, _ := NewLocal(LocalConfig{Root: dir, URL: "/media", Public: true})
	private.Put(ctx, "reports/q3 ü.pdf", strings.NewReader("%PDF-report"), PutOptions{})
	private.Put(ctx, "pages/x.html", strings.NewReader("<script>alert(1)</script>"), PutOptions{})

	if _, err := private.URL("reports/q3 ü.pdf"); !errors.Is(err, ErrPrivate) {
		t.Errorf("private URL = %v", err)
	}
	if url, err := public.URL("reports/q3 ü.pdf"); err != nil || url != "/media/reports/q3%20%C3%BC.pdf" {
		t.Errorf("public URL = %q, %v", url, err)
	}
	if _, err := public.TemporaryURL(ctx, "reports/q3 ü.pdf", time.Minute); !errors.Is(err, ErrNoKey) {
		t.Errorf("TemporaryURL without key = %v", err)
	}

	signed, err := private.TemporaryURL(ctx, "reports/q3 ü.pdf", time.Minute)
	if err != nil || !strings.HasPrefix(signed, "https://app.example.com/files/reports/q3%20%C3%BC.pdf?expires=") {
		t.Fatalf("TemporaryURL = %q, %v", signed, err)
	}
	u, _ := url.Parse(signed)
	h := private.Handler()
	target := strings.TrimPrefix(u.RequestURI(), "/files")
	rec := serve(h, "GET", target)
	if rec.Code != 200 || rec.Body.String() != "%PDF-report" || rec.Header().Get("Content-Type") != "application/pdf" ||
		rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("signed GET = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	req := httptest.NewRequest("GET", target, nil)
	req.Header.Set("Range", "bytes=0-3")
	ranged := httptest.NewRecorder()
	h.ServeHTTP(ranged, req)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "%PDF" {
		t.Errorf("range = %d %q", ranged.Code, ranged.Body.String())
	}

	query := u.Query()
	tampered := func(change func(url.Values)) string {
		q := url.Values{}
		for k, v := range query {
			q[k] = append([]string(nil), v...)
		}
		change(q)
		return u.EscapedPath()[len("/files"):] + "?" + q.Encode()
	}
	for name, target := range map[string]string{
		"unsigned":      "/reports/q3%20%C3%BC.pdf",
		"other file":    "/pages/x.html?" + query.Encode(),
		"later expiry":  tampered(func(q url.Values) { q.Set("expires", "99999999999") }),
		"bad signature": tampered(func(q url.Values) { q.Set("signature", "AAAA") }),
		"no expiry":     tampered(func(q url.Values) { q.Del("expires") }),
	} {
		if rec := serve(h, "GET", target); rec.Code != http.StatusForbidden {
			t.Errorf("%s = %d, want 403", name, rec.Code)
		}
	}
	expired := private.signer.sign("reports/q3 ü.pdf", time.Now().Add(-time.Second))
	if rec := serve(h, "GET", "/reports/q3%20%C3%BC.pdf?"+expired); rec.Code != http.StatusForbidden {
		t.Errorf("expired = %d", rec.Code)
	}
	if rec := serve(h, "POST", target); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}

	ph := public.Handler()
	page := serve(ph, "GET", "/pages/x.html")
	if page.Code != 200 || !strings.Contains(page.Header().Get("Content-Security-Policy"), "sandbox") || page.Header().Get("Cache-Control") != "" {
		t.Errorf("public HTML = %d %v", page.Code, page.Header())
	}
	for _, target := range []string{"/missing.txt", "/../secret", "/reports", "/"} {
		if rec := serve(ph, "GET", target); rec.Code != http.StatusNotFound {
			t.Errorf("public %s = %d", target, rec.Code)
		}
	}
	private.Put(ctx, "data.unknownext", strings.NewReader("raw"), PutOptions{})
	if rec := serve(ph, "GET", "/data.unknownext"); rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("unknown type = %q", rec.Header().Get("Content-Type"))
	}
}

func TestListSkipsTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	disk, _ := NewLocal(LocalConfig{Root: dir})
	os.WriteFile(filepath.Join(dir, tempPrefix+"abc"), []byte("partial"), 0o644)
	disk.Put(context.Background(), "real.txt", strings.NewReader("x"), PutOptions{})
	list, err := disk.List(context.Background(), "")
	if err != nil || len(list) != 1 || list[0].Path != "real.txt" {
		t.Errorf("List = %v, %v", list, err)
	}
}
