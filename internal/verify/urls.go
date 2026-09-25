package verify

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

type urlParts struct {
	scheme, netloc, path, params, query, fragment string
}

func isSchemeChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

func usesParams(scheme string) bool {
	switch scheme {
	case "", "ftp", "hdl", "prospero", "http", "imap", "https", "shttp", "rtsp", "rtspu", "sip", "sips", "mms", "sftp", "tel":
		return true
	}
	return false
}

func usesRelative(scheme string) bool {
	switch scheme {
	case "", "ftp", "http", "gopher", "nntp", "imap", "wais", "file", "https", "shttp", "mms",
		"prospero", "rtsp", "rtspu", "sftp", "svn", "svn+ssh", "ws", "wss":
		return true
	}
	return false
}

func usesNetloc(scheme string) bool {
	switch scheme {
	case "", "ftp", "http", "gopher", "nntp", "telnet", "imap", "wais", "file", "mms", "https", "shttp",
		"snews", "prospero", "rtsp", "rtspu", "rsync", "svn", "svn+ssh", "sftp", "nfs", "git", "git+ssh", "ws", "wss":
		return true
	}
	return false
}

// parseURL mirrors Python's urllib.parse.urlparse, which never fails on the URLs a browser reports.
func parseURL(raw, defaultScheme string) urlParts {
	raw = strings.TrimLeft(raw, controlAndSpace())
	raw = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(raw)
	parts := urlParts{scheme: defaultScheme}
	if i := strings.IndexByte(raw, ':'); i > 0 && isASCIILetter(raw[0]) {
		valid := true
		for j := 0; j < i; j++ {
			if !isSchemeChar(raw[j]) {
				valid = false
				break
			}
		}
		if valid {
			parts.scheme, raw = strings.ToLower(raw[:i]), raw[i+1:]
		}
	}
	if strings.HasPrefix(raw, "//") {
		end := len(raw)
		for _, delim := range "/?#" {
			if at := strings.IndexRune(raw[2:], delim); at >= 0 && at+2 < end {
				end = at + 2
			}
		}
		parts.netloc, raw = raw[2:end], raw[end:]
	}
	if before, after, found := strings.Cut(raw, "#"); found {
		raw, parts.fragment = before, after
	}
	if before, after, found := strings.Cut(raw, "?"); found {
		raw, parts.query = before, after
	}
	if usesParams(parts.scheme) && strings.Contains(raw, ";") {
		start := strings.LastIndexByte(raw, '/')
		if at := strings.IndexByte(raw[max(start, 0):], ';'); at >= 0 {
			at += max(start, 0)
			raw, parts.params = raw[:at], raw[at+1:]
		}
	}
	parts.path = raw
	return parts
}

func controlAndSpace() string {
	var b strings.Builder
	for c := 0; c <= 0x20; c++ {
		b.WriteByte(byte(c))
	}
	return b.String()
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// hostname is Python's ParseResult.hostname: lower-cased, without user info, port or brackets.
func (p urlParts) hostname() (string, bool) {
	hostinfo := p.netloc
	if at := strings.LastIndexByte(hostinfo, '@'); at >= 0 {
		hostinfo = hostinfo[at+1:]
	}
	var host string
	if _, bracketed, found := strings.Cut(hostinfo, "["); found {
		host, _, _ = strings.Cut(bracketed, "]")
	} else {
		host, _, _ = strings.Cut(hostinfo, ":")
	}
	if host == "" {
		return "", false
	}
	name, zone, hasZone := strings.Cut(host, "%")
	host = strings.ToLower(name)
	if hasZone {
		host += "%" + zone
	}
	return host, true
}

func (p urlParts) unparse() string {
	url := p.path
	if p.params != "" {
		url += ";" + p.params
	}
	switch {
	case p.netloc != "":
		if url != "" && url[0] != '/' {
			url = "/" + url
		}
		url = "//" + p.netloc + url
	case strings.HasPrefix(url, "//"):
		url = "//" + url
	case p.scheme != "" && usesNetloc(p.scheme) && (url == "" || url[0] == '/'):
		url = "//" + url
	}
	if p.scheme != "" {
		url = p.scheme + ":" + url
	}
	if p.query != "" {
		url += "?" + p.query
	}
	if p.fragment != "" {
		url += "#" + p.fragment
	}
	return url
}

// sameURL compares host without a leading "www.", path without trailing slashes, and query.
func sameURL(actual, expected string) bool {
	key := func(raw string) [3]string {
		parts := parseURL(raw, "")
		host, _ := parts.hostname()
		return [3]string{strings.TrimPrefix(host, "www."), strings.TrimRight(parts.path, "/"), parts.query}
	}
	return key(actual) == key(expected)
}

// joinURL mirrors Python's urllib.parse.urljoin.
func joinURL(base, ref string) string {
	if base == "" {
		return ref
	}
	if ref == "" {
		return base
	}
	b := parseURL(base, "")
	u := parseURL(ref, b.scheme)
	if u.scheme != b.scheme || !usesRelative(u.scheme) {
		return ref
	}
	if usesNetloc(u.scheme) {
		if u.netloc != "" {
			return u.unparse()
		}
		u.netloc = b.netloc
	}
	if u.path == "" && u.params == "" {
		u.path, u.params = b.path, b.params
		if u.query == "" {
			u.query = b.query
		}
		return u.unparse()
	}
	baseParts := strings.Split(b.path, "/")
	if baseParts[len(baseParts)-1] != "" {
		baseParts = baseParts[:len(baseParts)-1]
	}
	var segments []string
	if strings.HasPrefix(u.path, "/") {
		segments = strings.Split(u.path, "/")
	} else {
		segments = append(baseParts, strings.Split(u.path, "/")...)
		if len(segments) > 2 {
			inner := segments[1 : len(segments)-1]
			kept := make([]string, 0, len(inner))
			for _, segment := range inner {
				if segment != "" {
					kept = append(kept, segment)
				}
			}
			segments = append(append([]string{segments[0]}, kept...), segments[len(segments)-1])
		}
	}
	var resolved []string
	for _, segment := range segments {
		switch segment {
		case "..":
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
		case ".":
		default:
			resolved = append(resolved, segment)
		}
	}
	if last := segments[len(segments)-1]; last == "." || last == ".." {
		resolved = append(resolved, "")
	}
	u.path = strings.Join(resolved, "/")
	if u.path == "" {
		u.path = "/"
	}
	return u.unparse()
}

// unquotePlus mirrors Python's urllib.parse.unquote_plus: "+" is a space, %XX runs decode as UTF-8 with replacement.
func unquotePlus(s string) string {
	return unquote(strings.ReplaceAll(s, "+", " "))
}

func unquote(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var out strings.Builder
	for i := 0; i < len(s); {
		if s[i] >= utf8.RuneSelf {
			end := i
			for end < len(s) && s[end] >= utf8.RuneSelf {
				end++
			}
			out.WriteString(s[i:end])
			i = end
			continue
		}
		end := i
		for end < len(s) && s[end] < utf8.RuneSelf {
			end++
		}
		out.WriteString(decodeReplace(unquoteToBytes(s[i:end])))
		i = end
	}
	return out.String()
}

func unquoteToBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]) {
			out = append(out, unhex(s[i+1])<<4|unhex(s[i+2]))
			i += 2
			continue
		}
		out = append(out, s[i])
	}
	return out
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

// decodeReplace decodes UTF-8 like Python's errors="replace": one U+FFFD per maximal invalid subpart.
func decodeReplace(b []byte) string {
	var out strings.Builder
	for i := 0; i < len(b); {
		if r, size := utf8.DecodeRune(b[i:]); r != utf8.RuneError || size > 1 {
			out.WriteRune(r)
			i += size
			continue
		}
		out.WriteRune(utf8.RuneError)
		i += invalidLength(b[i:])
	}
	return out.String()
}

// invalidLength is the length of the maximal prefix of b that is a valid start of a UTF-8 sequence, at least 1.
func invalidLength(b []byte) int {
	lead := b[0]
	var need int
	lo, hi := byte(0x80), byte(0xbf)
	switch {
	case lead >= 0xc2 && lead <= 0xdf:
		need = 1
	case lead == 0xe0:
		need, lo = 2, 0xa0
	case lead >= 0xe1 && lead <= 0xef:
		need = 2
		if lead == 0xed {
			hi = 0x9f
		}
	case lead == 0xf0:
		need, lo = 3, 0x90
	case lead >= 0xf1 && lead <= 0xf3:
		need = 3
	case lead == 0xf4:
		need, hi = 3, 0x8f
	default:
		return 1
	}
	n := 1
	for n <= need && n < len(b) && b[n] >= lo && b[n] <= hi {
		n++
		lo, hi = 0x80, 0xbf
	}
	return n
}

// queryValue is the first non-empty value of key in query, like parse_qs(query).get(key, [""])[0].
func queryValue(query, key string) string {
	for _, pair := range strings.Split(query, "&") {
		name, value, found := strings.Cut(pair, "=")
		if !found || value == "" {
			continue
		}
		if unquote(strings.ReplaceAll(name, "+", " ")) == key {
			return unquote(strings.ReplaceAll(value, "+", " "))
		}
	}
	return ""
}

// urlsafeBase64Contains reports whether needle is in the URL-safe base64 decoding of encoded, decoded as Python's
// base64.urlsafe_b64decode does with padding added; any decoding error is false.
func urlsafeBase64Contains(encoded, needle string) bool {
	if !isASCII(encoded) {
		return false
	}
	padded := encoded + strings.Repeat("=", (4-len(encoded)%4)%4)
	data, ok := decodeBase64Lenient(padded)
	return ok && bytes.Contains(data, []byte(needle))
}

func decodeBase64Lenient(s string) ([]byte, bool) {
	var out []byte
	quad, pads := 0, 0
	var left byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '=' {
			if quad >= 2 {
				pads++
				if quad+pads >= 4 {
					return out, true
				}
			}
			continue
		}
		v, ok := base64Value(c)
		if !ok {
			continue
		}
		pads = 0
		switch quad {
		case 0:
			quad, left = 1, v
		case 1:
			out = append(out, left<<2|v>>4)
			quad, left = 2, v&0x0f
		case 2:
			out = append(out, left<<4|v>>2)
			quad, left = 3, v&0x03
		default:
			out = append(out, left<<6|v)
			quad, left = 0, 0
		}
	}
	return out, quad == 0
}

func base64Value(c byte) (byte, bool) {
	switch {
	case c >= 'A' && c <= 'Z':
		return c - 'A', true
	case c >= 'a' && c <= 'z':
		return c - 'a' + 26, true
	case c >= '0' && c <= '9':
		return c - '0' + 52, true
	case c == '+' || c == '-':
		return 62, true
	case c == '/' || c == '_':
		return 63, true
	}
	return 0, false
}
