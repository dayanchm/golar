package cli

import (
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"

	"github.com/dayanchm/golar/internal/project"
)

// requestNames turns "StorePost", "store_post" or "store-post-request" into
// the struct name StorePostRequest and the file name store_post_request.go.
func requestNames(value string) (structName, fileName string, err error) {
	name, err := normalizeMigrationName(value)
	if err != nil {
		return "", "", fmt.Errorf("invalid request name %q: use ASCII letters, digits, _ or -", value)
	}
	name = strings.TrimSuffix(name, "_request")
	if name == "" || name == "request" {
		return "", "", fmt.Errorf("invalid request name %q", value)
	}
	name += "_request"
	return migrationStruct(name), name + ".go", nil
}

const requestTemplate = `package requests

import "github.com/dayanchm/golar"

// %[1]s is the body of a request. Bind and validate it in a handler:
//
//	var in requests.%[1]s
//	if err := c.Validate(&in); err != nil {
//		return err // 400, 403 or 422
//	}
type %[1]s struct {
	// Name string ` + "`" + `json:"name" validate:"required,max=255"` + "`" + `
}

// Authorize reports whether the current user may make this request. Return
// httpcontext.ErrForbidden (github.com/dayanchm/golar/http/context) to answer 403.
func (r *%[1]s) Authorize(c *golar.Context) error { return nil }

// Prepare normalizes input, such as trimming strings, before validation.
func (r *%[1]s) Prepare(c *golar.Context) error { return nil }
`

func (c *CLI) makeRequest(args []string) error {
	flags := flag.NewFlagSet("make:request", flag.ContinueOnError)
	flags.SetOutput(c.Err)
	root := flags.String("dir", c.Dir, "project directory")
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		if name != "" || flags.NArg() != 1 {
			return fmt.Errorf("expected one request name")
		}
		name = flags.Arg(0)
	}
	structName, fileName, err := requestNames(name)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(*root) {
		*root = filepath.Join(c.Dir, *root)
	}
	if _, err = project.Load(*root); err != nil {
		return err
	}
	dir := filepath.Join(*root, "app", "requests")
	for _, path := range []string{filepath.Join(*root, "app"), dir} {
		if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("request: refusing symlink %s", path)
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	body, err := format.Source(fmt.Appendf(nil, requestTemplate, structName))
	if err != nil {
		return err
	}
	path := filepath.Join(dir, fileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("request: %s already exists", path)
	}
	if err != nil {
		return err
	}
	_, err = file.Write(body)
	if err = errors.Join(err, file.Close()); err != nil {
		_ = os.Remove(path)
		return err
	}
	fmt.Fprintln(c.Out, "Created", path)
	return nil
}
