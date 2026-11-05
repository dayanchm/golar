package storage_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dayanchm/golar/core"
	httpcontext "github.com/dayanchm/golar/http/context"
	"github.com/dayanchm/golar/storage"
)

type avatarRequest struct {
	Avatar *multipart.FileHeader `form:"avatar" validate:"required,image,max_size=1KB"`
}

// TestUploadFlow wires an upload endpoint the way an application would and
// drives it over HTTP: bind and validate, store, then fetch with a signed URL.
func TestUploadFlow(t *testing.T) {
	disk, err := storage.NewLocal(storage.LocalConfig{Root: t.TempDir(), URL: "/files", Key: bytes.Repeat([]byte("k"), 32)})
	if err != nil {
		t.Fatal(err)
	}
	app := core.New()
	app.Post("/avatar", func(c *httpcontext.Context) error {
		var in avatarRequest
		if err := c.Validate(&in); err != nil {
			return err
		}
		name, err := storage.PutUpload(c.Context(), disk, "avatars", in.Avatar)
		if err != nil {
			return err
		}
		link, err := disk.TemporaryURL(c.Context(), name, time.Minute)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, map[string]string{"path": name, "url": link})
	})
	mux := http.NewServeMux()
	mux.Handle("/files/", http.StripPrefix("/files", disk.Handler()))
	mux.Handle("/", app)
	server := httptest.NewServer(mux)
	defer server.Close()

	post := func(name string, body []byte) (*http.Response, map[string]any) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		part, _ := w.CreateFormFile("avatar", name)
		part.Write(body)
		w.Close()
		resp, err := http.Post(server.URL+"/avatar", w.FormDataContentType(), &buf)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 100)...)
	resp, out := post("me.png", png)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload = %d %v", resp.StatusCode, out)
	}
	link, _ := url.Parse(out["url"].(string))
	got, err := http.Get(server.URL + link.RequestURI())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if got.StatusCode != 200 || !bytes.Equal(body, png) || got.Header.Get("Content-Type") != "image/png" {
		t.Errorf("signed GET = %d, %d bytes, %q", got.StatusCode, len(body), got.Header.Get("Content-Type"))
	}
	if unsigned, _ := http.Get(server.URL + link.Path); unsigned.StatusCode != http.StatusForbidden {
		t.Errorf("unsigned GET = %d", unsigned.StatusCode)
	}

	resp, out = post("evil.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	errs, _ := out["errors"].(map[string]any)
	if resp.StatusCode != http.StatusUnprocessableEntity || errs["avatar"] == nil {
		t.Errorf("SVG upload = %d %v", resp.StatusCode, out)
	}
	resp, out = post("big.png", append(png, make([]byte, 2000)...))
	if resp.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(out["errors"].(map[string]any)["avatar"].([]any)[0].(string), "1KB") {
		t.Errorf("oversized upload = %d %v", resp.StatusCode, out)
	}
	if files, _ := disk.List(t.Context(), "avatars"); len(files) != 1 {
		t.Errorf("stored files = %v", files)
	}
}
