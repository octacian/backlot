package compose

import (
	"path"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/octacian/backlot/api/v1"
)

func fileValue(base v1.Value, parts ...v1.Value) v1.Value {
	if parts == nil {
		parts = []v1.Value{}
	}
	return v1.Value{FilePath: &v1.FilePathValue{Base: base, Parts: parts}}
}

func TestFilePathLibrarySemantics(t *testing.T) {
	pieces := []string{"", ".", "..", "/", "//", "a", "a//b/", "../../x", "/root", "雪/space x", `a\b`, "http://a?q#f"}
	for _, a := range pieces {
		for _, b := range pieces {
			for _, c := range pieces {
				operands := []string{a, b, c}
				for _, runtime := range []v1.Runtime{v1.Native, v1.Container} {
					want := filepath.Join(operands...)
					if runtime == v1.Container {
						want = path.Join(operands...)
					}
					got, err := Render(fileValue(literal(a)), runtime, operands)
					if err != nil || got != want {
						t.Fatalf("%s %q: got %q, want %q: %v", runtime, operands, got, want, err)
					}
				}
			}
		}
	}
	for _, base := range pieces {
		got := resolve(t, fileValue(literal(base)))
		if got != filepath.Join(base) {
			t.Fatalf("empty parts: %q -> %q", base, got)
		}
	}
}

func TestFilePathBoundsAndDiscardedOperands(t *testing.T) {
	identity := func(v v1.Value) (v1.Value, error) { return v, nil }
	unknown := v1.Value{Ref: &v1.Reference{Kind: "instance", Field: "id"}}
	huge := strings.Repeat("x", 4*MaxBytes)
	// The raw segment is larger than the cap, but a later parent removes it.
	for _, v := range []v1.Value{
		fileValue(literal(huge), literal(".."), literal("result")),
		fileValue(literal("/base"), literal(huge), literal(".."), literal("../result")),
	} {
		if got := resolve(t, v); got != "result" && got != "/result" {
			t.Fatal(got)
		}
	}
	partial, err := Resolve(fileValue(literal(huge), unknown), v1.Native, identity)
	if err != nil || partial.FilePath == nil {
		t.Fatal("partial discarded prematurely", err)
	}
	got, err := Resolve(partial, v1.Native, func(v v1.Value) (v1.Value, error) { return literal("../result"), nil })
	if err != nil || got.Literal == nil || *got.Literal != "result" {
		t.Fatal("partial clean", got, err)
	}
	for _, base := range []string{"", "/"} {
		for _, extra := range []int{0, 1} {
			v := fileValue(literal(base), unknown)
			value := strings.Repeat("x", MaxBytes-len(base)+extra)
			got, err := Resolve(v, v1.Native, func(v v1.Value) (v1.Value, error) { return literal(value), nil })
			if (err == nil) != (extra == 0) || err == nil && len(*got.Literal) != MaxBytes {
				t.Fatal("hydrated bytes", len(base), extra, err)
			}
		}
	}
	for _, v := range []v1.Value{
		fileValue(literal("private-canary\x00"), literal("..")),
		fileValue(unknown, literal("private-canary\x00"), literal("..")),
	} {
		if _, err := Resolve(v, v1.Native, identity); err != ErrInvalid || strings.Contains(err.Error(), "private-canary") {
			t.Fatal("discarded NUL", err)
		}
	}
	secret := literal("discarded")
	secret.Secret = true
	got, err = Resolve(fileValue(secret, literal(".."), literal("safe")), v1.Native, identity)
	if err != nil || !got.Secret || got.Literal == nil || *got.Literal != "safe" {
		t.Fatal("discarded sensitivity", got, err)
	}
	// Measure allocations with preexisting operands, not the test's large input.
	result := testing.Benchmark(func(b *testing.B) {
		for b.Loop() {
			if _, err := filePath([]string{huge, "..", "result"}, v1.Container, nil); err != nil {
				b.Fatal(err)
			}
		}
	})
	if result.AllocedBytesPerOp() > 4096 {
		t.Fatalf("cleaned-away input allocated %d bytes", result.AllocedBytesPerOp())
	}
}

func TestURLPathPartsAndNormalizationBounds(t *testing.T) {
	for _, tc := range []struct {
		operands []string
		want     string
	}{
		{[]string{"https://example.test/base%20x/"}, "https://example.test/base%20x/"},
		{[]string{"https://example.test/base/", "", "/a/", "", "/b/"}, "https://example.test/base/a/b/"},
		{[]string{"http://a/", "/", "", "x"}, "http://a/x"},
		{[]string{"http://a/base", "a//b/", "/c 雪/"}, "http://a/base/a//b/c%20%E9%9B%AA/"},
		{[]string{"http://a/base" + strings.Repeat("/", MaxBytes), "x"}, "http://a/base/x"},
	} {
		got, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, tc.operands)
		if err != nil || got != tc.want {
			t.Fatalf("want %q, got %q: %v", tc.want, got, err)
		}
	}
	if _, err := Render(v1.Value{URLPath: &v1.URLPathValue{}}, v1.Native, []string{"invalid"}); err != ErrInvalid {
		t.Fatal("identity skipped base validation", err)
	}
	unknown := v1.Value{Ref: &v1.Reference{Kind: "instance", Field: "id"}}
	v := v1.Value{URLPath: &v1.URLPathValue{Base: literal("http://a/base" + strings.Repeat("/", MaxBytes)), Parts: []v1.Value{unknown}}}
	partial, err := Resolve(v, v1.Native, func(v v1.Value) (v1.Value, error) { return v, nil })
	if err != nil {
		t.Fatal("partial boundary may shrink", err)
	}
	got, err := Resolve(partial, v1.Native, func(v v1.Value) (v1.Value, error) { return literal("x"), nil })
	if err != nil || got.Literal == nil || *got.Literal != "http://a/base/x" {
		t.Fatal(got, err)
	}
	for _, bad := range []string{"..", "%252e%252e", "%252f", "x\x00"} {
		v.URLPath = &v1.URLPathValue{Base: unknown, Parts: []v1.Value{literal("safe"), literal(bad), literal("tail")}}
		if _, err := Resolve(v, v1.Native, func(v v1.Value) (v1.Value, error) { return v, nil }); err != ErrInvalid {
			t.Fatal("known part not validated", err)
		}
	}
}

func TestPartialFilePathIrreducibleBounds(t *testing.T) {
	unknown := v1.Value{Ref: &v1.Reference{Kind: "instance", Field: "id"}}
	identity := func(v v1.Value) (v1.Value, error) { return v, nil }
	for _, tc := range []struct {
		base      string
		size      int
		hydration string
		valid     bool
	}{
		{"/", MaxBytes, "", false},
		{"/", MaxBytes - 1, "", true},
		{".", MaxBytes, "", true},
		{"../a", MaxBytes - 2, "..", false},
		{"../a", MaxBytes - 3, "..", true},
	} {
		value := fileValue(literal(tc.base), unknown, literal(strings.Repeat("x", tc.size)))
		partial, err := Resolve(value, v1.Native, identity)
		if (err == nil) != tc.valid {
			t.Fatalf("base=%q terminal bytes=%d: %v", tc.base, tc.size, err)
		}
		if !tc.valid {
			continue
		}
		got, err := Resolve(partial, v1.Native, func(v v1.Value) (v1.Value, error) { return literal(tc.hydration), nil })
		if err != nil || got.Literal == nil || len(*got.Literal) != MaxBytes {
			t.Fatalf("exact-cap hydration: %v", err)
		}
	}
	// A later unknown parent may remove a huge earlier segment, even below a root.
	huge := literal(strings.Repeat("x", 2*MaxBytes))
	huge.Secret = true
	partial, err := Resolve(fileValue(literal("/"), huge, unknown), v1.Native, identity)
	if err != nil || partial.FilePath == nil || !partial.Secret {
		t.Fatal("removable known prefix", err)
	}
	got, err := Resolve(partial, v1.Native, func(v v1.Value) (v1.Value, error) { return literal("../leaf"), nil })
	if err != nil || got.Literal == nil || *got.Literal != "/leaf" || !got.Secret {
		t.Fatal("parent shortening", err)
	}
	// An unknown first operand can select a relative result despite a rooted suffix.
	value := fileValue(unknown, literal("/"+strings.Repeat("x", MaxBytes)))
	if _, err := Resolve(value, v1.Native, identity); err != nil {
		t.Fatal("uncertain root rejected", err)
	}
}

func TestDeferredURLIdentityBounds(t *testing.T) {
	base := "http://private-canary.test/" + strings.Repeat("雪", 120000)
	empty := fileValue(literal(""))
	empty.Secret = true
	value := v1.Value{URLPath: &v1.URLPathValue{Base: literal(base), Parts: []v1.Value{empty}}}
	identity := func(v v1.Value) (v1.Value, error) { return v, nil }
	for _, runtime := range []v1.Runtime{"", v1.Native, v1.Container} {
		got, err := Resolve(value, runtime, identity)
		if err != nil || !got.Secret {
			t.Fatalf("identity context=%q: %v", runtime, err)
		}
		if runtime == "" {
			if got.URLPath == nil || got.URLPath.Parts[0].FilePath == nil {
				t.Fatal("consumer context was folded")
			}
			got, err = Resolve(got, v1.Native, identity)
		}
		if err != nil || got.Literal == nil || *got.Literal != base {
			t.Fatal("raw identity changed", err)
		}
	}
	value.URLPath.Parts = append(value.URLPath.Parts, literal("/"))
	for _, runtime := range []v1.Runtime{"", v1.Native} {
		if _, err := Resolve(value, runtime, identity); err != ErrInvalid || strings.Contains(err.Error(), "private-canary") {
			t.Fatal("unavoidable escaping not checked privately", err)
		}
	}
	// A definitely nonempty slash requires a separator even if a later operand
	// remains deferred and can be empty; it cannot preserve an at-cap raw base.
	base = "http://a/" + strings.Repeat("x", MaxBytes-len("http://a/"))
	value.URLPath = &v1.URLPathValue{Base: literal(base), Parts: []v1.Value{literal("/"), empty}}
	if _, err := Resolve(value, "", identity); err != ErrInvalid {
		t.Fatal("mandatory final separator lost", err)
	}
}
