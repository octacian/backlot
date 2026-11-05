package compose

import (
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func literal(s string) v1.Value { return v1.Value{Literal: &s} }
func formatValue(template string, values ...v1.Value) v1.Value {
	return v1.Value{Format: &v1.FormatValue{Template: template, Values: values}}
}
func resolve(t *testing.T, v v1.Value) string {
	t.Helper()
	result, err := Resolve(v, v1.Native, func(v v1.Value) (v1.Value, error) { return v, nil })
	if err != nil || result.Literal == nil {
		t.Fatalf("resolve: %v %+v", err, result)
	}
	return *result.Literal
}
func TestFormat(t *testing.T) {
	if got := resolve(t, formatValue("%%")); got != "%" {
		t.Fatal(got)
	}
	special := "space %s %% $HOME & ? = 雪"
	if got := resolve(t, formatValue("%%%s / %s", literal(special), literal(""))); got != "%"+special+" / " {
		t.Fatal(got)
	}
	for _, template := range []string{"%", "%d", "%02s", "%.2s", "%[1]s", "%+s", "% s", "%q", "%v", "%ss%s"} {
		v := formatValue(template, literal("private-canary"))
		if _, err := Resolve(v, v1.Native, func(v v1.Value) (v1.Value, error) { return v, nil }); err == nil || strings.Contains(err.Error(), "private-canary") {
			t.Fatalf("format %q: %v", template, err)
		}
	}
	if _, err := Render(formatValue("%s"), v1.Native, nil); err == nil {
		t.Fatal("missing operand")
	}
	if _, err := Render(formatValue("%%"), v1.Native, []string{"extra"}); err == nil {
		t.Fatal("extra operand")
	}
}
func TestHostPort(t *testing.T) {
	for _, tc := range []struct{ host, port, want string }{{"localhost", "8080", "localhost:8080"}, {"::1", "443", "[::1]:443"}, {"2001:db8::2", "65535", "[2001:db8::2]:65535"}} {
		v := v1.Value{HostPort: &v1.HostPortValue{Host: literal(tc.host), Port: literal(tc.port)}}
		if got := resolve(t, v); got != tc.want {
			t.Fatal(got)
		}
	}
	for _, tc := range [][2]string{{"[::1]", "443"}, {"::1%zone", "443"}, {"user@host", "80"}, {"host:80", "80"}, {"a..b", "80"}, {"-host", "80"}, {"host", "0"}, {"host", "65536"}, {"host", "+80"}, {"host", " 80"}} {
		if _, err := Render(v1.Value{HostPort: &v1.HostPortValue{}}, v1.Native, tc[:]); err == nil {
			t.Fatal(tc)
		}
	}
}
func TestURLPath(t *testing.T) {
	for _, tc := range []struct{ base, path, want string }{
		{"https://example.test/base", "api/", "https://example.test/base/api/"},
		{"https://example.test/base/", "/api", "https://example.test/base/api"},
		{"https://[::1]:8443/base", "a b/雪/@x:+$&=", "https://[::1]:8443/base/a%20b/%E9%9B%AA/@x:+$&="},
		{"https://example.test/base/", "", "https://example.test/base/"},
		{"http://example.test/base//", "a//b/", "http://example.test/base/a//b/"},
		{"http://example.test", "encoded%20space/%25", "http://example.test/encoded%20space/%25"},
	} {
		got, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{tc.base, tc.path})
		if err != nil || got != tc.want {
			t.Fatalf("%+v: %q %v", tc, got, err)
		}
	}
	for _, path := range []string{"..", "a/../b", "./a", "%2e%2e/a", ".%2e/a", "a%2fb", "a%5cb", "%252e%252e/a", "%252fb", "//evil/a", "a?query", "a#fragment", "a\\b", "%2e%2e/%25", "%2e/%25", "safe/%2e%2e/%25", "%252e%252e/%2525"} {
		if _, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{"https://example.test/base", path}); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	for _, base := range []string{"/relative", "ftp://example.test", "http://user:private-canary@example.test", "http://example.test?", "http://example.test#", "http://example.test:0", "http://example.test:", "http://example.test/a/../b", "http://example.test/a%2fb", "https://example.test/%2e%2e/%25", "https://example.test/%2e/%25", "https://example.test/safe/%2e%2e/%25"} {
		_, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{base, "a"})
		if err == nil || strings.Contains(err.Error(), "private-canary") {
			t.Fatalf("base %q: %v", base, err)
		}
	}
}
func TestCompositionByteBounds(t *testing.T) {
	for _, n := range []int{MaxBytes - 1, MaxBytes, MaxBytes + 1} {
		text := strings.Repeat("x", n)
		_, err := Resolve(formatValue("%s", literal(text)), v1.Native, func(v v1.Value) (v1.Value, error) { return v, nil })
		if (err == nil) != (n <= MaxBytes) {
			t.Fatalf("format length %d: %v", n, err)
		}
	}
	// Every Unicode byte must count, including expansion by net/url.
	base := "https://example.test/"
	prefix := strings.Repeat("x", MaxBytes-len(base)-9)
	for _, suffix := range []string{prefix + "雪", prefix + "雪x"} {
		got, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{base, suffix})
		want := len(suffix) == len(prefix)+3
		if (err == nil) != want {
			t.Fatalf("escaped byte bound: %d %v", len(suffix), err)
		}
		if err == nil && len(got) != MaxBytes {
			t.Fatal(len(got))
		}
	}
	// Old literals do not acquire the new composition cap.
	if got := resolve(t, literal(strings.Repeat("x", MaxBytes+1))); len(got) != MaxBytes+1 {
		t.Fatal(len(got))
	}
	unknown := v1.Value{Ref: &v1.Reference{Kind: "instance", Field: "id"}}
	value, err := Resolve(formatValue("%s-%s", literal("known"), unknown), v1.Native, func(v v1.Value) (v1.Value, error) { return v, nil })
	if err != nil || value.Format == nil || value.Format.Values[1].Ref == nil {
		t.Fatal("lost unresolved provenance", err)
	}
	_, err = Resolve(formatValue("%s%s", literal(strings.Repeat("x", MaxBytes+1)), unknown), v1.Native, func(v v1.Value) (v1.Value, error) { return v, nil })
	if err == nil {
		t.Fatal("known lower bound not checked")
	}
}

func TestHostPortExactByteBounds(t *testing.T) {
	for _, host := range []string{"localhost", "::1"} {
		separator := 1
		if strings.Contains(host, ":") {
			separator = 3
		}
		for _, extra := range []int{0, 1} {
			// Leading zeros retain string semantics while the decimal value remains 80.
			port := strings.Repeat("0", MaxBytes-len(host)-separator-2+extra) + "80"
			got, err := Render(v1.Value{HostPort: &v1.HostPortValue{}}, v1.Native, []string{host, port})
			if (err == nil) != (extra == 0) {
				t.Fatalf("host_port byte bound: host=%s extra=%d err=%v", host, extra, err)
			}
			if err == nil && len(got) != MaxBytes {
				t.Fatal(len(got))
			}
		}
	}
}

func TestPartialConstructorByteBounds(t *testing.T) {
	unknown := v1.Value{Ref: &v1.Reference{Kind: "instance", Field: "id"}}
	identity := func(v v1.Value) (v1.Value, error) { return v, nil }
	for _, extra := range []int{0, 1, 2} {
		port := strings.Repeat("0", MaxBytes-4+extra) + "80"
		value := v1.Value{HostPort: &v1.HostPortValue{Host: unknown, Port: formatValue("%s", literal(port))}}
		partial, err := Resolve(value, v1.Native, identity)
		if (err == nil) != (extra == 0) {
			t.Fatalf("partial host_port extra=%d: %v", extra, err)
		}
		if err == nil {
			got, err := Resolve(partial, v1.Native, func(v v1.Value) (v1.Value, error) { return literal("a"), nil })
			if err != nil || got.Literal == nil || len(*got.Literal) != MaxBytes {
				t.Fatalf("host_port cap witness: %v", err)
			}
		}
	}
	for _, leading := range []string{"", "/"} {
		for _, escaped := range []string{"x", "%25", "雪"} {
			decoded, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{"http://a", escaped})
			if err != nil {
				t.Fatal(err)
			}
			expansion := len(decoded) - len("http://a/")
			for _, extra := range []int{0, 1} {
				path := leading + strings.Repeat("x", MaxBytes-9-expansion+extra) + escaped
				value := v1.Value{URLPath: &v1.URLPathValue{Base: unknown, Parts: []v1.Value{formatValue("%s", literal(path))}}}
				partial, err := Resolve(value, v1.Native, identity)
				if (err == nil) != (extra == 0) {
					t.Fatalf("partial url_path leading=%q escaped=%q extra=%d: %v", leading, escaped, extra, err)
				}
				if err == nil {
					got, err := Resolve(partial, v1.Native, func(v v1.Value) (v1.Value, error) { return literal("http://a"), nil })
					if err != nil || got.Literal == nil || len(*got.Literal) != MaxBytes {
						t.Fatalf("url_path cap witness: %v", err)
					}
				}
			}
		}
	}
	// An unknown suffix may be empty, preserving a base already at the cap.
	base := "http://a/" + strings.Repeat("x", MaxBytes-len("http://a/"))
	value := v1.Value{URLPath: &v1.URLPathValue{Base: literal(base), Parts: []v1.Value{unknown}}}
	partial, err := Resolve(value, v1.Native, identity)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(partial, v1.Native, func(v v1.Value) (v1.Value, error) { return literal(""), nil })
	if err != nil || got.Literal == nil || *got.Literal != base {
		t.Fatalf("empty suffix cap witness: %v", err)
	}
}

func TestURLPathMixedEncodingRejection(t *testing.T) {
	unknown := v1.Value{Ref: &v1.Reference{Kind: "resource", Name: "origin", Field: "url"}}
	for _, seed := range []string{"%2e", "%2E%2e", ".%2e", "%2f", "%2F", "%5c", "%5C", "%00", "%0a", "%0d"} {
		encoded := seed
		for depth := 0; depth < 6; depth++ {
			percent := "%25"
			for percentDepth := 0; percentDepth < 4; percentDepth++ {
				paths := []string{encoded + "/" + percent, percent + "/" + encoded, "safe/" + encoded + "/" + percent + "/tail"}
				if seed != "%2e" && seed != "%2E%2e" && seed != ".%2e" {
					paths = append(paths, percent+encoded, encoded+percent)
				}
				for _, path := range paths {
					for _, role := range []string{"suffix", "base"} {
						value := v1.Value{URLPath: &v1.URLPathValue{Base: unknown, Parts: []v1.Value{literal(path)}}}
						hydration := "https://example.test/base"
						if role == "base" {
							value.URLPath = &v1.URLPathValue{Base: literal(hydration + "/" + path), Parts: []v1.Value{unknown}}
							hydration = "x"
						}
						for _, hydrated := range []bool{false, true} {
							_, err := Resolve(value, v1.Native, func(v v1.Value) (v1.Value, error) {
								if hydrated {
									return literal(hydration), nil
								}
								return v, nil
							})
							if err != ErrInvalid {
								t.Fatalf("role=%s hydrated=%v path=%q: want sanitized rejection, got %v", role, hydrated, path, err)
							}
						}
					}
				}
				percent = strings.ReplaceAll(percent, "%", "%25")
			}
			encoded = strings.ReplaceAll(encoded, "%", "%25")
		}
	}
}

func TestURLPathMixedEncodingPreservesBenignPaths(t *testing.T) {
	for _, tc := range []struct{ path, escaped string }{
		{"%25", "%25"},
		{"%25/%25", "%25/%25"},
		{"%25done/雪/", "%25done/%E9%9B%AA/"},
		{"x%2520y", "x%2520y"},
		{"%25/%2520/雪/", "%25/%2520/%E9%9B%AA/"},
		{"%2520/%25G0/%252525", "%2520/%25G0/%252525"},
		{"%25%252e/%252e%25", "%25%252e/%252e%25"},
		{strings.Repeat("%25", 1024), strings.Repeat("%25", 1024)},
	} {
		for _, role := range []string{"suffix", "base"} {
			base, path := "https://example.test/base", tc.path
			want := base + "/" + tc.escaped
			if role == "base" {
				base, path = base+"/"+tc.path, "x"
				want = strings.TrimRight(want, "/") + "/x"
			}
			got, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{base, path})
			if err != nil || got != want {
				t.Fatalf("role=%s path=%q: %q %v", role, tc.path, got, err)
			}
		}
	}
	for _, path := range []string{"%", "%0", "%GG", "%25/%"} {
		if _, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{"http://a", path}); err != ErrInvalid {
			t.Fatalf("malformed initial escape %q: %v", path, err)
		}
	}
	if _, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{"http://a", "%" + strings.Repeat("25", 66)}); err != ErrInvalid {
		t.Fatalf("excessive encoding depth: %v", err)
	}
}
