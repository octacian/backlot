// Package compose renders bounded declared string compositions without evaluation.
package compose

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"path"
	"path/filepath"
	"slices"
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
// An unknown runtime (empty string) preserves filesystem constructions for shared consumers.
func Resolve(v v1.Value, runtime v1.Runtime, reference func(v1.Value) (v1.Value, error)) (v1.Value, error) {
	resolved, err := resolveValue(v, runtime, reference)
	if err != nil || runtime != "" || resolved.Literal != nil || resolved.Ref != nil {
		return resolved, err
	}
	// Validate both eventual consumers without replacing the captured tree or
	// re-reading references; deferred children can hide known parent failures.
	for _, consumer := range []v1.Runtime{v1.Native, v1.Container} {
		if _, err := resolveValue(resolved, consumer, func(v v1.Value) (v1.Value, error) { return v, nil }); err != nil {
			return v1.Value{}, err
		}
	}
	return resolved, nil
}

func resolveValue(v v1.Value, runtime v1.Runtime, reference func(v1.Value) (v1.Value, error)) (v1.Value, error) {
	if v.Ref != nil {
		return reference(v)
	}
	operands := v.Operands()
	if v.Format == nil && v.HostPort == nil && v.URLPath == nil && v.FilePath == nil {
		return v, nil
	}
	resolved := make([]v1.Value, len(operands))
	texts := make([]string, len(operands))
	known := true
	for i, child := range operands {
		var err error
		resolved[i], err = resolveValue(child, runtime, reference)
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
		v.URLPath = &v1.URLPathValue{Base: resolved[0], Parts: resolved[1:]}
	case v.FilePath != nil:
		v.FilePath = &v1.FilePathValue{Base: resolved[0], Parts: resolved[1:]}
	}
	if v.FilePath != nil {
		for i, operand := range resolved {
			if operand.Literal != nil && strings.ContainsRune(texts[i], 0) {
				return v1.Value{}, ErrInvalid
			}
		}
	}
	if known && (v.FilePath == nil || runtime != "") {
		text, err := Render(v, runtime, texts)
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
		hostSize, portSize, separator := 1, 1, 1
		if resolved[0].Literal != nil {
			hostSize = len(texts[0])
			if strings.Contains(texts[0], ":") {
				separator = 3
			}
		}
		if resolved[1].Literal != nil {
			portSize = len(texts[1])
		}
		if hostSize > MaxBytes-portSize-separator {
			return v1.Value{}, ErrInvalid
		}
	}
	if v.URLPath != nil || v.FilePath != nil {
		knownParts := make([]bool, len(resolved))
		for i := range resolved {
			knownParts[i] = resolved[i].Literal != nil
		}
		var err error
		if v.URLPath != nil {
			_, err = urlParts(texts, knownParts)
		} else {
			_, err = filePath(texts, v1.Native, knownParts)
		}
		if err != nil {
			return v1.Value{}, err
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
func Render(v v1.Value, runtime v1.Runtime, operands []string) (string, error) {
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
		if len(operands) == 0 {
			return "", ErrInvalid
		}
		return urlParts(operands, nil)
	case v.FilePath != nil:
		if len(operands) == 0 || runtime == "" {
			return "", ErrInvalid
		}
		return filePath(operands, runtime, nil)
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
			if depth == 0 {
				return false
			}
			// A decoded literal percent must not hide valid escapes elsewhere in this layer.
			var layer strings.Builder
			layer.Grow(len(current))
			prefix, rest, _ := strings.Cut(current, "%")
			layer.WriteString(prefix)
			for part := range strings.SplitSeq(rest, "%") {
				piece, err := url.PathUnescape("%" + part)
				if err != nil {
					layer.WriteByte('%')
					layer.WriteString(part)
				} else {
					layer.WriteString(piece)
				}
			}
			decoded = layer.String()
		}
		if decoded == current {
			return true
		}
		if strings.ContainsAny(decoded, "\\\x00\r\n") || strings.Count(decoded, "/") != strings.Count(current, "/") {
			return false
		}
		current = decoded
	}
	return false
}

func baseURL(text string) (*url.URL, error) {
	if strings.ContainsAny(text, "?#\\\x00\r\n") {
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

// urlParts measures the final escaped path before allocating it. Partial checks
// use only a lower bound: unknown later parts can remove trailing boundary slashes.
func urlParts(operands []string, known []bool) (string, error) {
	complete := known == nil
	base := operands[0]
	var u *url.URL
	var err error
	if complete || known[0] {
		u, err = baseURL(base)
		if err != nil {
			return "", err
		}
	} else {
		u = &url.URL{Scheme: "http", Host: "a"}
	}
	paths := []string{u.Path}
	appended := false
	for i, part := range operands[1:] {
		if !complete && !known[i+1] {
			for len(paths) > 1 && strings.TrimRight(paths[len(paths)-1], "/") == "" {
				paths = paths[:len(paths)-1]
			}
			paths[len(paths)-1] = strings.TrimRight(paths[len(paths)-1], "/")
			continue
		}
		if !safePath(part) {
			return "", ErrInvalid
		}
		if part == "" {
			continue
		}
		decoded, err := url.PathUnescape(part)
		if err != nil {
			return "", ErrInvalid
		}
		for len(paths) > 1 && strings.TrimRight(paths[len(paths)-1], "/") == "" {
			paths = paths[:len(paths)-1]
		}
		paths[len(paths)-1] = strings.TrimRight(paths[len(paths)-1], "/")
		paths = append(paths, strings.TrimPrefix(decoded, "/"))
		appended = true
	}
	if complete && !appended {
		if len(base) > MaxBytes {
			return "", ErrInvalid
		}
		return base, nil
	}
	// Deferred parts may all be empty, preserving the raw base without escaping.
	if !complete && !appended && known[0] && len(base) <= MaxBytes {
		return "", nil
	}
	size := len(u.Scheme) + 3 + len(u.Host)
	// Any append still requires one slash when deferred boundaries erased the tail.
	if !complete && len(paths) == 1 && (appended || known[0] && len(base) > MaxBytes) {
		size++
	}
	for i, part := range paths {
		n := escapedSize(part)
		if i > 0 {
			n++
		}
		if n > MaxBytes-size {
			return "", ErrInvalid
		}
		size += n
	}
	if !complete {
		return "", nil
	}
	u.Path = strings.Join(paths, "/")
	u.RawPath = ""
	return u.String(), nil
}

// filePath cancels segments backwards before joining, so even enormous inputs
// cleaned away by later parents never allocate an oversized intermediate string.
// Supported native hosts and containers use slash separators; the selected Go
// library owns the resulting path. This does not add Windows host support.
func filePath(operands []string, runtime v1.Runtime, known []bool) (string, error) {
	rooted, nonempty, prefixKnown := false, false, true
	depth, leadingParents := 0, 0
	for i, part := range operands {
		if strings.ContainsRune(part, 0) {
			return "", ErrInvalid
		}
		if known != nil && !known[i] {
			prefixKnown = false
		}
		if !prefixKnown {
			continue
		}
		if !nonempty && part != "" {
			rooted = strings.HasPrefix(part, "/")
			nonempty = true
		}
		if known != nil {
			for segment := range strings.SplitSeq(part, "/") {
				switch segment {
				case "", ".":
				case "..":
					if depth > 0 {
						depth--
					} else if !rooted {
						leadingParents++
					}
				default:
					depth++
				}
			}
		}
	}
	parents, size := 0, 0
	var segments []string
	for i := len(operands) - 1; i >= 0; i-- {
		if known != nil && !known[i] {
			// Unknown parents may erase earlier normal segments, but cannot erase
			// the known root, leading parents, or surviving later segments.
			parents = leadingParents
			break
		}
		rest := operands[i]
		for rest != "" {
			index := strings.LastIndexByte(rest, '/')
			segment := rest[index+1:]
			if index < 0 {
				rest = ""
			} else {
				rest = rest[:index]
			}
			switch segment {
			case "", ".":
			case "..":
				parents++
			default:
				if parents > 0 {
					parents--
					continue
				}
				if len(segment) > MaxBytes-size {
					return "", ErrInvalid
				}
				size += len(segment)
				if len(segments) > 0 {
					size++
				}
				if size > MaxBytes {
					return "", ErrInvalid
				}
				segments = append(segments, segment)
			}
		}
	}
	if !rooted {
		for ; parents > 0; parents-- {
			size += 2
			if len(segments) > 0 {
				size++
			}
			if size > MaxBytes {
				return "", ErrInvalid
			}
			segments = append(segments, "..")
		}
	}
	if rooted {
		if size == MaxBytes {
			return "", ErrInvalid
		}
		segments = append(segments, "/")
	} else if len(segments) == 0 && nonempty {
		segments = append(segments, ".")
	}
	if known != nil {
		return "", nil
	}
	slices.Reverse(segments)
	if runtime == v1.Container {
		return path.Join(segments...), nil
	}
	return filepath.Join(segments...), nil
}
