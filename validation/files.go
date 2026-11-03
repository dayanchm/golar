package validation

import (
	"context"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"reflect"
	"strconv"
	"strings"
)

var fileHeaderType = reflect.TypeFor[multipart.FileHeader]()

// uploadedFile returns the *multipart.FileHeader a field holds.
func uploadedFile(v reflect.Value) (*multipart.FileHeader, bool) {
	if !v.IsValid() || v.Type() != fileHeaderType {
		return nil, false
	}
	if v.CanAddr() {
		return v.Addr().Interface().(*multipart.FileHeader), true
	}
	file := v.Interface().(multipart.FileHeader)
	return &file, true
}

// SniffContentType returns the media type of an upload detected from its first
// 512 bytes, without parameters, such as "image/png". The client's
// Content-Type header and file name are ignored, since the client controls
// them.
func SniffContentType(file *multipart.FileHeader) (string, error) {
	f, err := file.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", err
	}
	mediaType, _, _ := mime.ParseMediaType(http.DetectContentType(head[:n]))
	return mediaType, nil
}

// parseSize reads sizes such as "512", "100KB", "2MB" or "1GB" (powers of 1024).
func parseSize(param string) (int64, error) {
	s := strings.ToUpper(strings.TrimSpace(param))
	multiplier := int64(1)
	for _, unit := range []struct {
		suffix string
		value  int64
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}} {
		if rest, ok := strings.CutSuffix(s, unit.suffix); ok {
			s, multiplier = strings.TrimSpace(rest), unit.value
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size %q; use bytes or KB, MB or GB", param)
	}
	return n * multiplier, nil
}

// imageTypes are the raster formats the image rule accepts. SVG is excluded
// because it can carry scripts.
var imageTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp"}

func fileRules() map[string]Rule {
	return map[string]Rule{
		"file": func(_ context.Context, f Field) (bool, error) {
			_, ok := uploadedFile(f.Value)
			return ok, nil
		},
		"max_size": fileSizeRule(func(size, limit int64) bool { return size <= limit }),
		"min_size": fileSizeRule(func(size, limit int64) bool { return size >= limit }),
		"mimetypes": func(_ context.Context, f Field) (bool, error) {
			return sniffedIn(f, strings.Fields(f.Param))
		},
		"image": func(_ context.Context, f Field) (bool, error) {
			return sniffedIn(f, imageTypes)
		},
		"extensions": func(_ context.Context, f Field) (bool, error) {
			file, ok := uploadedFile(f.Value)
			if !ok {
				return false, nil
			}
			ext := strings.TrimPrefix(strings.ToLower(path.Ext(file.Filename)), ".")
			for _, allowed := range strings.Fields(f.Param) {
				if ext != "" && ext == strings.ToLower(strings.TrimPrefix(allowed, ".")) {
					return true, nil
				}
			}
			return false, nil
		},
	}
}

func fileSizeRule(compare func(size, limit int64) bool) Rule {
	return func(_ context.Context, f Field) (bool, error) {
		file, ok := uploadedFile(f.Value)
		if !ok {
			return false, nil
		}
		limit, err := parseSize(f.Param)
		if err != nil {
			return false, err
		}
		return compare(file.Size, limit), nil
	}
}

// sniffedIn reports whether the upload's detected type is one of types, which
// may end in "/*" to match a whole family such as image/*.
func sniffedIn(f Field, types []string) (bool, error) {
	file, ok := uploadedFile(f.Value)
	if !ok {
		return false, nil
	}
	detected, err := SniffContentType(file)
	if err != nil {
		return false, err
	}
	for _, allowed := range types {
		if family, ok := strings.CutSuffix(allowed, "/*"); ok {
			if strings.HasPrefix(detected, family+"/") {
				return true, nil
			}
		} else if detected == allowed {
			return true, nil
		}
	}
	return false, nil
}
