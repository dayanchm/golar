package validation

import (
	"bytes"
	"errors"
	"mime/multipart"
	"strings"
	"testing"
)

// upload builds a FileHeader the way a multipart request does. The client's
// Content-Type is deliberately misleading, since the rules must ignore it.
func upload(t *testing.T, name string, body []byte) *multipart.FileHeader {
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

var (
	pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 100)...)
	pdfBytes = []byte("%PDF-1.7\n" + strings.Repeat("x", 200))
	svgBytes = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
)

func TestFileRules(t *testing.T) {
	type form struct {
		Avatar   *multipart.FileHeader   `json:"avatar" validate:"required,file,image,extensions=png jpg,max_size=1KB"`
		Document *multipart.FileHeader   `json:"document" validate:"mimetypes=application/pdf,min_size=100B"`
		Photos   []*multipart.FileHeader `json:"photos" validate:"max=3,each,image"`
		Any      *multipart.FileHeader   `json:"any" validate:"mimetypes=image/*"`
	}
	valid := form{
		Avatar:   upload(t, "me.PNG", pngBytes),
		Document: upload(t, "cv.pdf", pdfBytes),
		Photos:   []*multipart.FileHeader{upload(t, "a.png", pngBytes)},
		Any:      upload(t, "x.png", pngBytes),
	}
	if err := Validate(valid); err != nil {
		t.Fatalf("valid uploads: %v", err)
	}

	invalid := form{
		Avatar:   upload(t, "evil.png", svgBytes),                       // named .png, really SVG
		Document: upload(t, "cv.pdf", []byte("%PDF-1 tiny")),              // too small
		Photos:   []*multipart.FileHeader{upload(t, "p.png", pdfBytes)}, // PDF in a photo list
		Any:      upload(t, "x.pdf", pdfBytes),
	}
	var verr *Error
	if err := Validate(invalid); !errors.As(err, &verr) {
		t.Fatalf("error = %v", err)
	}
	if got := verr.Errors["avatar"]; len(got) != 1 || got[0] != "The avatar field must be an image." {
		t.Errorf("avatar = %v", got)
	}
	if got := verr.Errors["document"]; len(got) != 1 || got[0] != "The document field must be at least 100B." {
		t.Errorf("document = %v", got)
	}
	if got := verr.Errors["photos.0"]; len(got) != 1 {
		t.Errorf("photos.0 = %v", got)
	}
	if got := verr.Errors["any"]; len(got) != 1 || got[0] != "The any field must be a file of type: image/*." {
		t.Errorf("any = %v", got)
	}

	big := form{Avatar: upload(t, "big.jpg", append([]byte("\xff\xd8\xff"), make([]byte, 2000)...))}
	if err := Validate(big); !errors.As(err, &verr) || verr.Errors["avatar"][0] != "The avatar field must not be larger than 1KB." {
		t.Errorf("oversized = %v", err)
	}
	wrongExt := form{Avatar: upload(t, "me.gif", pngBytes)}
	if err := Validate(wrongExt); !errors.As(err, &verr) || len(verr.Errors["avatar"]) != 1 {
		t.Errorf("extension = %v", err)
	}
	if err := Validate(form{}); !errors.As(err, &verr) || verr.Errors["avatar"][0] != "The avatar field is required." || len(verr.Errors) != 1 {
		t.Errorf("missing = %v", err)
	}
	notFile := struct {
		V string `json:"v" validate:"file"`
	}{"x"}
	if err := Validate(notFile); !errors.As(err, &verr) {
		t.Errorf("string as file = %v", err)
	}
	for _, tag := range []string{"max_size=lots", "max_size=-1", "mimetypes", "extensions", "image=x"} {
		if _, err := New().parseTag(tag); err == nil {
			t.Errorf("tag %q accepted", tag)
		}
	}
	for param, want := range map[string]int64{"512": 512, "2KB": 2048, "3 mb": 3 << 20, "1GB": 1 << 30, "10B": 10} {
		if got, err := parseSize(param); err != nil || got != want {
			t.Errorf("parseSize(%q) = %d, %v", param, got, err)
		}
	}
	if got, _ := SniffContentType(upload(t, "x.txt", pngBytes)); got != "image/png" {
		t.Errorf("sniffed = %q", got)
	}
}
