package pdf

import "testing"

// TestContentRoots verifies each kind of holder: the streams yielded, the
// resources they come with, and that Replace stores into the slot each stream
// was found in.
func TestContentRoots(t *testing.T) {
	stream := func(body string) PDFDict {
		s := NewPDFDict()
		s.SetStream([]byte(body))
		return s
	}
	dict := func(kv ...any) PDFDict {
		d := NewPDFDict()
		for i := 0; i < len(kv); i += 2 {
			d.Entries.Set(kv[i].(string), kv[i+1].(PDFValue))
		}
		return d
	}
	collect := func(d PDFDict) (ContentKind, []ContentRoot) {
		var roots []ContentRoot
		kind := ContentRoots(d, func(r ContentRoot) { roots = append(roots, r) })
		return kind, roots
	}
	res := dict("Font", NewPDFDict())

	t.Run("page with one stream inherits resources", func(t *testing.T) {
		parent := dict("Type", PDFName{Value: "Pages"}, "Resources", res)
		page := dict("Type", PDFName{Value: "Page"}, "Parent", parent, "Contents", stream("a"))
		kind, roots := collect(page)
		if kind != ContentPage || len(roots) != 1 {
			t.Fatalf("got kind %d with %d roots, want a page with 1", kind, len(roots))
		}
		if ValuePointer(roots[0].Resources.Entries) != ValuePointer(res.Entries) {
			t.Error("page stream does not carry the inherited resources")
		}
		roots[0].Replace(stream("b"))
		if got := string(page.Entries.Get("Contents").(PDFDict).Stream()); got != "b" {
			t.Errorf("Contents after Replace = %q, want %q", got, "b")
		}
	})

	t.Run("page with an array skips non-streams", func(t *testing.T) {
		contents := PDFArray{NewPDFDict(), stream("a"), PDFInteger(1)}
		kind, roots := collect(dict("Type", PDFName{Value: "Page"}, "Contents", contents))
		if kind != ContentPage || len(roots) != 1 {
			t.Fatalf("got kind %d with %d roots, want a page with 1", kind, len(roots))
		}
		roots[0].Replace(stream("b"))
		if got := string(contents[1].(PDFDict).Stream()); got != "b" {
			t.Errorf("contents[1] after Replace = %q, want %q", got, "b")
		}
	})

	t.Run("page without contents", func(t *testing.T) {
		kind, roots := collect(dict("Type", PDFName{Value: "Page"}, "Contents", NewPDFDict()))
		if kind != ContentPage || len(roots) != 0 {
			t.Fatalf("got kind %d with %d roots, want a page with none", kind, len(roots))
		}
	})

	for _, tc := range []struct {
		name string
		key  string
		val  PDFValue
		want ContentKind
	}{
		{"tiling pattern", "PatternType", PDFInteger(1), ContentPattern},
		{"form XObject", "Subtype", PDFName{Value: "Form"}, ContentForm},
	} {
		t.Run(tc.name+" is its own stream", func(t *testing.T) {
			d := stream("a")
			d.Entries.Set(tc.key, tc.val)
			d.Entries.Set("Resources", res)
			kind, roots := collect(d)
			if kind != tc.want || len(roots) != 1 {
				t.Fatalf("got kind %d with %d roots, want kind %d with 1", kind, len(roots), tc.want)
			}
			if ValuePointer(roots[0].Stream.Entries) != ValuePointer(d.Entries) ||
				ValuePointer(roots[0].Resources.Entries) != ValuePointer(res.Entries) {
				t.Error("root is not the dictionary itself with its own resources")
			}
			roots[0].Replace(stream("b"))
			if got := string(d.Stream()); got != "a" {
				t.Errorf("Replace changed the stream to %q; it must do nothing", got)
			}
		})
	}

	t.Run("Type 3 font glyph procedures", func(t *testing.T) {
		procs := dict("a", stream("a"), "b", PDFInteger(1))
		font := dict("Subtype", PDFName{Value: "Type3"}, "CharProcs", procs, "Resources", res)
		kind, roots := collect(font)
		if kind != ContentType3 || len(roots) != 1 {
			t.Fatalf("got kind %d with %d roots, want a Type 3 font with 1", kind, len(roots))
		}
		if ValuePointer(roots[0].Resources.Entries) != ValuePointer(res.Entries) {
			t.Error("glyph procedure does not carry the font's resources")
		}
		roots[0].Replace(stream("z"))
		if got := string(procs.Entries.Get("a").(PDFDict).Stream()); got != "z" {
			t.Errorf("CharProcs /a after Replace = %q, want %q", got, "z")
		}
	})

	t.Run("anything else", func(t *testing.T) {
		if kind, roots := collect(dict("Subtype", PDFName{Value: "Form"})); kind != ContentNone || len(roots) != 0 {
			t.Errorf("a form without a stream: got kind %d with %d roots, want none", kind, len(roots))
		}
	})
}
