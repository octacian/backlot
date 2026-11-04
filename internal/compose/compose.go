// Package compose renders bounded declared string compositions without evaluation.
package compose

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	v1 "github.com/octacian/backlot/api/v1"
)

// MaxBytes bounds each new composition's rendered output, including URL escaping.
const MaxBytes = 1 << 20

// ErrInvalid is a sanitized construction or rendered-size failure.
var ErrInvalid = errors.New("invalid composition inputs or output exceeds 1 MiB")

// FormatCount validates the complete restricted format without exposing its text.
func FormatCount(literal string) (int, error) {
	if strings.ContainsRune(literal, 0) {
		return 0, ErrInvalid
	}
	count := 0
	for i := 0; i < len(literal); i++ {
		if literal[i] != '%' {
			continue
		}
		i++
		if i == len(literal) {
			return 0, ErrInvalid
		}
		switch literal[i] {
		case 's':
			count++
		case '%':
		default:
			return 0, ErrInvalid
		}
	}
	return count, nil
}

// Resolve replaces reference leaves and checks each known composition before use.
// Unresolved operands remain typed; sensitivity includes every operand.
func Resolve(v v1.Value, reference func(v1.Value) (v1.Value, error)) (v1.Value, error) {
	if v.Ref != nil {
		return reference(v)
	}
	operands := v.Operands()
	if v.Format == nil && v.HostPort == nil && v.URLPath == nil {
		return v, nil
	}
	resolved := make([]v1.Value, len(operands))
	texts := make([]string, len(operands))
	known := true
	for i, child := range operands {
		var err error
		resolved[i], err = Resolve(child, reference)
		if err != nil {
			return v1.Value{}, err
		}
		v.Secret = v.Secret || resolved[i].Secret
		if resolved[i].Literal == nil {
			known = false
		} else {
			texts[i] = *resolved[i].Literal
		}
	}
	switch {
	case v.Format != nil:
		f := *v.Format
		f.Values = resolved
		v.Format = &f
	case v.HostPort != nil:
		v.HostPort = &v1.HostPortValue{Host: resolved[0], Port: resolved[1]}
	case v.URLPath != nil:
		v.URLPath = &v1.URLPathValue{Base: resolved[0], Path: resolved[1]}
	}
	if known {
		text, err := Render(v, texts)
		if err != nil {
			return v1.Value{}, err
		}
		return v1.Value{Literal: &text, Secret: v.Secret}, nil
	}
	// Validate known constructor operands even when another leaf is unresolved.
	if v.HostPort != nil {
		if resolved[0].Literal != nil && !validHost(texts[0]) {
			return v1.Value{}, ErrInvalid
		}
		if resolved[1].Literal != nil && !validPort(texts[1]) {
			return v1.Value{}, ErrInvalid
		}
	}
	if v.URLPath != nil {
		if resolved[0].Literal != nil {
			if _, err := baseURL(texts[0]); err != nil {
				return v1.Value{}, ErrInvalid
			}
		}
		if resolved[1].Literal != nil {
			if !safePath(texts[1]) {
				return v1.Value{}, ErrInvalid
			}
			decoded, _ := url.PathUnescape(texts[1])
			if escapedSize(decoded) > MaxBytes {
				return v1.Value{}, ErrInvalid
			}
		}
	}
	if v.Format != nil {
		n, err := FormatCount(v.Format.Template)
		if err != nil || n != len(resolved) {
			return v1.Value{}, ErrInvalid
		}
		// Unknown strings have a zero lower bound; reject already oversized known output.
		if _, err := format(v.Format.Template, texts); err != nil {
			return v1.Value{}, err
		}
	}
	return v, nil
}

// Render checks byte accounting before allocating the composed output.
func Render(v v1.Value, operands []string) (string, error) {
	switch {
	case v.Format != nil:
		return format(v.Format.Template, operands)
	case v.HostPort != nil:
		if len(operands) != 2 || !validHost(operands[0]) || !validPort(operands[1]) {
			return "", ErrInvalid
		}
		separator := 1
		if strings.Contains(operands[0], ":") {
			separator = 3
		}
		if len(operands[0]) > MaxBytes-len(operands[1])-separator {
			return "", ErrInvalid
		}
		return net.JoinHostPort(operands[0], operands[1]), nil
	case v.URLPath != nil:
		if len(operands) != 2 {
			return "", ErrInvalid
		}
		return appendURL(operands[0], operands[1])
	}
	return "", ErrInvalid
}

func format(literal string, values []string) (string, error) {
	count, err := FormatCount(literal)
	if err != nil || count != len(values) {
		return "", ErrInvalid
	}
	size, index := 0, 0
	for i := 0; i < len(literal); i++ {
		n := 1
		if literal[i] == '%' {
			i++
			if literal[i] == 's' {
				n = len(values[index])
				index++
			}
		}
		if n > MaxBytes-size {
			return "", ErrInvalid
		}
		size += n
	}
	var output strings.Builder
	output.Grow(size)
	index = 0
	for i := 0; i < len(literal); i++ {
		if literal[i] != '%' {
			output.WriteByte(literal[i])
			continue
		}
		i++
		if literal[i] == '%' {
			output.WriteByte('%')
		} else {
			output.WriteString(values[index])
			index++
		}
	}
	return output.String(), nil
}

func validPort(port string) bool {
	if port == "" {
		return false
	}
	for i := range port {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n > 0
}
func validHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == ""
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := range label {
			c := label[i]
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}
func safePath(path string) bool {
	if strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\?#\x00\r\n") {
		return false
	}
	current := path
	for depth := 0; depth <= 64; depth++ {
		for _, segment := range strings.Split(current, "/") {
			if segment == "." || segment == ".." {
				return false
			}
		}
		decoded, err := url.PathUnescape(current)
		if err != nil {
			return false
		}
		if decoded == current {
			return true
		}
		if strings.ContainsAny(decoded, "\\\x00\r\n") || strings.Count(decoded, "/") != strings.Count(current, "/") {
			return false
		}
		// A decoded literal percent is safe unless it begins another encoded layer.
		if !strings.Contains(decoded, "%") {
			current = decoded
			continue
		}
		next, err := url.PathUnescape(decoded)
		if err != nil {
			return true
		}
		if next == decoded {
			return true
		}
		current = decoded
	}
	return false
}

func baseURL(text string) (*url.URL, error) {
	if len(text) > MaxBytes || strings.ContainsAny(text, "?#\\\x00\r\n") {
		return nil, ErrInvalid
	}
	u, err := url.Parse(text)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Opaque != "" || u.User != nil || !validHost(u.Hostname()) || strings.HasSuffix(u.Host, ":") || u.Port() != "" && !validPort(u.Port()) {
		return nil, ErrInvalid
	}
	// net/url validated the authority; inspect its original path without rendering escapes.
	pathStart := len(u.Scheme) + 3 + len(u.Host)
	if pathStart > len(text) || !safePath(text[pathStart:]) {
		return nil, ErrInvalid
	}
	return u, nil
}

// Let net/url own escaping; the byte table only measures its expansion.
var pathBytes = func() [256]int {
	var sizes [256]int
	for i := range sizes {
		sizes[i] = len((&url.URL{Path: string([]byte{byte(i)})}).EscapedPath())
	}
	return sizes
}()

func escapedSize(path string) int {
	size := 0
	for i := 0; i < len(path); i++ {
		if pathBytes[path[i]] > MaxBytes-size {
			return MaxBytes + 1
		}
		size += pathBytes[path[i]]
	}
	return size
}
func appendURL(base, path string) (string, error) {
	u, err := baseURL(base)
	if err != nil || !safePath(path) {
		return "", ErrInvalid
	}
	if path == "" {
		return base, nil
	}
	// Treat suffix as a URL path, retaining valid existing escapes through net/url.
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return "", ErrInvalid
	}
	prefix := strings.TrimRight(u.Path, "/")
	suffix := strings.TrimPrefix(decoded, "/")
	size := len(u.Scheme) + 3 + len(u.Host) + escapedSize(prefix) + 1 + escapedSize(suffix)
	if size > MaxBytes {
		return "", ErrInvalid
	}
	u.Path = prefix + "/" + suffix
	u.RawPath = ""
	return u.String(), nil
}
