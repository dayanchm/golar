package context

import (
	"mime"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// IP returns the client address. X-Forwarded-For is used only when the
// connection comes from a trusted proxy: the result is the rightmost address
// that is not itself a trusted proxy, so clients cannot spoof it.
func (c *Context) IP() string {
	remote, ok := parseAddr(c.Request.RemoteAddr)
	if !ok {
		return c.Request.RemoteAddr
	}
	if !c.trusted(remote) {
		return remote.String()
	}
	client := remote
	forwarded := strings.Split(strings.Join(c.Request.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		addr, ok := parseAddr(strings.TrimSpace(forwarded[i]))
		if !ok {
			break
		}
		client = addr
		if !c.trusted(addr) {
			break
		}
	}
	return client.String()
}

// IsTLS reports whether the client connected over HTTPS, directly or through
// a trusted proxy that sets X-Forwarded-Proto.
func (c *Context) IsTLS() bool {
	if c.Request.TLS != nil {
		return true
	}
	remote, ok := parseAddr(c.Request.RemoteAddr)
	return ok && c.trusted(remote) && strings.EqualFold(c.Request.Header.Get("X-Forwarded-Proto"), "https")
}

func (c *Context) trusted(addr netip.Addr) bool {
	for _, prefix := range c.config.TrustedProxies {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// parseAddr parses "host:port", "[v6]:port" or a bare address.
func parseAddr(value string) (netip.Addr, bool) {
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	addr, err := netip.ParseAddr(strings.Trim(value, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap().WithZone(""), true
}

// WantsJSON reports whether the Accept header prefers JSON over HTML.
func (c *Context) WantsJSON() bool {
	var jsonQ, htmlQ float64
	for part := range strings.SplitSeq(c.Request.Header.Get("Accept"), ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		q := 1.0
		if value, ok := params["q"]; ok {
			if q, err = strconv.ParseFloat(value, 64); err != nil {
				continue
			}
		}
		switch {
		case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
			jsonQ = max(jsonQ, q)
		case mediaType == "text/html":
			htmlQ = max(htmlQ, q)
		}
	}
	return jsonQ > 0 && jsonQ >= htmlQ
}

// Cookie returns a cookie's value or http.ErrNoCookie.
func (c *Context) Cookie(name string) (string, error) {
	cookie, err := c.Request.Cookie(name)
	if err != nil {
		return "", err
	}
	return cookie.Value, nil
}

// SetCookie sets a cookie with secure defaults: Path "/", HttpOnly,
// SameSite=Lax and Secure on HTTPS requests. A zero maxAge makes a session
// cookie. Use SetRawCookie for other attributes.
func (c *Context) SetCookie(name, value string, maxAge time.Duration) error {
	cookie := &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   c.IsTLS(),
	}
	if maxAge > 0 {
		cookie.MaxAge = max(1, int(maxAge/time.Second))
		cookie.Expires = time.Now().Add(maxAge).UTC()
	}
	return c.SetRawCookie(cookie)
}

// SetRawCookie sets a cookie as given. Unlike http.SetCookie, it returns an
// error for invalid cookies instead of dropping them.
func (c *Context) SetRawCookie(cookie *http.Cookie) error {
	if err := cookie.Valid(); err != nil {
		return err
	}
	http.SetCookie(c.Writer, cookie)
	return nil
}

// ClearCookie expires a cookie set by SetCookie.
func (c *Context) ClearCookie(name string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   c.IsTLS(),
	})
}
