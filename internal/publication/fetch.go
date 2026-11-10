package publication

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

func originURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawPath != "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" ||
		(u.Path != "" && u.Path != "/") || (u.Port() != "" && u.Port() != "443") {
		return nil, fmt.Errorf("origin must be one HTTPS origin on port 443")
	}
	u.Path = ""
	return u, nil
}

func assetPath(value string, origin *url.URL) (string, error) {
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" ||
		u.RawFragment != "" || u.Opaque != "" || u.RawPath != "" || u.EscapedPath() != u.Path ||
		strings.ContainsAny(u.Path, "\\\x00") || !strings.HasPrefix(u.Path, "/") ||
		strings.HasPrefix(u.Path, "//") || path.Clean(u.Path) != u.Path {
		return "", fmt.Errorf("invalid asset URL %q", value)
	}
	if u.IsAbs() {
		if u.Scheme != origin.Scheme || u.Host != origin.Host {
			return "", fmt.Errorf("asset escapes fixed origin")
		}
	} else if u.Host != "" {
		return "", fmt.Errorf("asset escapes fixed origin")
	}
	return u.Path, nil
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, prefix := range []string{
		"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4",
		"64:ff9b::/96", "64:ff9b:1::/48", "2001::/23", "2001:db8::/32", "2002::/16",
	} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

// Resolve once per connection, validate every answer, then dial an IP literal.
// No proxy or second DNS lookup can route an approved hostname to private IPs.
func publicDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || port != "443" {
		return nil, fmt.Errorf("invalid remote address")
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 || len(ips) > 16 {
		return nil, fmt.Errorf("invalid DNS answer count")
	}
	for _, ip := range ips {
		if !publicIP(ip) {
			return nil, fmt.Errorf("origin resolves to a non-public address")
		}
	}
	return (&net.Dialer{Timeout: 15 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

type remoteReader struct {
	origin *url.URL
	client *http.Client
}

func newRemoteReader(origin *url.URL) *remoteReader {
	transport := &http.Transport{
		Proxy: nil, DialContext: publicDial, DisableCompression: true,
		TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10, MaxIdleConnsPerHost: 1,
	}
	return &remoteReader{origin, &http.Client{
		Transport: transport, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return fmt.Errorf("redirects are forbidden") },
	}}
}

func (r *remoteReader) read(ctx context.Context, filename string, expected Proof, contentType string) ([]byte, error) {
	u := *r.origin
	u.Path = filename
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", filename, resp.StatusCode)
	}
	if encoding := resp.Header.Get("Content-Encoding"); encoding != "" && !strings.EqualFold(encoding, "identity") {
		return nil, fmt.Errorf("GET %s: unexpected content encoding", filename)
	}
	actualType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || actualType != contentType {
		return nil, fmt.Errorf("GET %s: incorrect Content-Type", filename)
	}
	if !cacheable(resp.Header.Values("Cache-Control")) {
		return nil, fmt.Errorf("GET %s: not publicly cacheable", filename)
	}
	if resp.ContentLength >= 0 && resp.ContentLength != expected.Bytes {
		return nil, fmt.Errorf("GET %s: incorrect Content-Length", filename)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, expected.Bytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != expected.Bytes || hash(data) != expected.SHA256 {
		return nil, fmt.Errorf("GET %s: size/SHA256 mismatch", filename)
	}
	return data, nil
}

// The CDN may replace the origin's year/immutable hint with public,max-age=86400.
// Token comparisons reject not-public and uncacheable directives, even with TTL.
func cacheable(headers []string) bool {
	public, maxAge, sharedAge := false, int64(-1), int64(-1)
	directives, ok := splitDirectives(strings.Join(headers, ","))
	if !ok {
		return false
	}
	for _, directive := range directives {
		name, value, hasValue := strings.Cut(strings.TrimSpace(directive), "=")
		name = strings.ToLower(strings.TrimSpace(name))
		switch name {
		case "private", "no-cache", "no-store":
			return false
		case "public":
			if hasValue {
				return false
			}
			public = true
		case "max-age", "s-maxage":
			value = strings.TrimSpace(value)
			if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
				value = value[1 : len(value)-1]
			}
			if !hasValue || value == "" || strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
				return false
			}
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return false
			}
			if name == "s-maxage" {
				if sharedAge >= 0 {
					return false
				}
				sharedAge = n
			} else {
				if maxAge >= 0 {
					return false
				}
				maxAge = n
			}
		}
	}
	if sharedAge >= 0 {
		return public && sharedAge > 0
	}
	return public && maxAge > 0
}

func splitDirectives(value string) ([]string, bool) {
	var directives []string
	quoted, escaped, start := false, false, 0
	for i, c := range value {
		if c < 32 || c == 127 {
			return nil, false
		}
		if escaped {
			escaped = false
			continue
		}
		if quoted && c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			quoted = !quoted
		}
		if c == ',' && !quoted {
			directives = append(directives, value[start:i])
			start = i + 1
		}
	}
	if quoted || escaped {
		return nil, false
	}
	return append(directives, value[start:]), true
}
