package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/pdfa"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// a2bSpec is PDF/A-2b's spec for tests that only read it.
var a2bSpec = func() *pdfa.Spec { s, _ := pdfa.For(pdf.A2B); return &s }()

// kv builds a dictionary from alternating keys and values.
func kv(pairs ...any) pdf.PDFDict {
	d := pdf.NewPDFDict()
	for i := 0; i < len(pairs); i += 2 {
		d.Entries.Set(pairs[i].(string), pairs[i+1].(pdf.PDFValue))
	}
	return d
}

func nm(s string) pdf.PDFName { return pdf.PDFName{Value: s} }

func TestUTF8Name(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Cyan#C2", "Cyan#C3#82"},
		{"Caf#C3#A9#E9", "Caf#C3#A9#C3#A9"},
	} {
		got, ok := utf8Name(nm(tc.in))
		if !ok || got.Value != tc.want {
			t.Errorf("utf8Name(%s) = %s, %v, want %s", tc.in, got.Value, ok, tc.want)
		}
	}
	if got, ok := utf8Name(nm("Caf#C3#A9")); ok || got.Value != "Caf#C3#A9" {
		t.Errorf("utf8Name of a valid name = %s, %v", got.Value, ok)
	}
}

func TestUnicodeNameFixer(t *testing.T) {
	font := kv("Type", nm("Font"), "BaseFont", nm("Caf#E9"))
	descriptor := kv("Type", nm("FontDescriptor"), "FontName", nm("Caf#E9"))
	roleMap := kv("P#E9", nm("H#E9"), "P#C3#A9", nm("P"), "Para", nm("P"))
	treeRoot := kv("Type", nm("StructTreeRoot"), "RoleMap", roleMap)
	typed := kv("Type", nm("StructElem"), "S", nm("P#E9"))
	untyped := kv("S", nm("P#E9"), "P", treeRoot)
	unrelated := kv("S", nm("P#E9"))
	attributes := kv("Colorants", kv("Spot#FF", pdf.PDFArray{}))
	separation := pdf.PDFArray{nm("Separation"), nm("Cyan#C2"), nm("DeviceCMYK"), pdf.NewPDFDict()}
	deviceN := pdf.PDFArray{nm("DeviceN"), pdf.PDFArray{nm("Cyan"), nm("Spot#FF"), pdf.PDFInteger(1)},
		nm("DeviceCMYK"), pdf.NewPDFDict(), attributes}
	short := pdf.PDFArray{nm("Separation")}
	root := kv("Type", nm("Catalog"), "StructTreeRoot", treeRoot,
		"Things", pdf.PDFArray{font, descriptor, typed, untyped, unrelated, separation, deviceN, short})
	trailer := kv("Root", root)

	changed, err := unicodeNameFixer{}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	for _, tc := range []struct {
		desc string
		got  pdf.PDFValue
		want string
	}{
		{"BaseFont", font.Entries.Get("BaseFont"), "Caf#C3#A9"},
		{"FontName", descriptor.Entries.Get("FontName"), "Caf#C3#A9"},
		{"typed S", typed.Entries.Get("S"), "P#C3#A9"},
		{"untyped S", untyped.Entries.Get("S"), "P#C3#A9"},
		{"S without a parent", unrelated.Entries.Get("S"), "P#E9"},
		{"Separation colorant", separation[1], "Cyan#C3#82"},
		{"DeviceN colorant", deviceN[1].(pdf.PDFArray)[1], "Spot#C3#BF"},
		{"role map value", roleMap.Entries.Get("Para"), "P"},
	} {
		if n, _ := tc.got.(pdf.PDFName); n.Value != tc.want {
			t.Errorf("%s = %v, want /%s", tc.desc, tc.got, tc.want)
		}
	}
	// The re-encoded role map key is taken, so the existing entry wins.
	if roleMap.Entries.Get("P#E9") != nil || roleMap.Entries.Get("P#C3#A9") != (nm("P")) {
		t.Errorf("role map = %v", roleMap.Entries)
	}
	colorants := attributes.Entries.Get("Colorants").(pdf.PDFDict)
	if colorants.Entries.Get("Spot#FF") != nil || colorants.Entries.Get("Spot#C3#BF") == nil {
		t.Errorf("Colorants = %v", colorants.Entries)
	}

	if changed, _ := (unicodeNameFixer{}).Fix(&trailer, nil); changed {
		t.Error("a second Fix changed the graph")
	}
	if !(unicodeNameFixer{}).Applies(pdf.Checks.Structure.NameNotUTF8) {
		t.Error("unicodeNameFixer does not apply to NameNotUTF8")
	}
}

func TestUnicodeNameFixerRenamesRoleMapKeys(t *testing.T) {
	roleMap := kv("P#E9", nm("H#E9"))
	trailer := kv("Root", kv("StructTreeRoot", kv("Type", nm("StructTreeRoot"), "RoleMap", roleMap)))
	if changed, _ := (unicodeNameFixer{}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix made no change")
	}
	if roleMap.Entries.Get("P#C3#A9") != (nm("H#C3#A9")) || roleMap.Entries.Len() != 1 {
		t.Errorf("role map = %v", roleMap.Entries)
	}
}

func TestPermissionsFixer(t *testing.T) {
	build := func() (trailer, perms, ref pdf.PDFDict) {
		perms = kv("UR", pdf.NewPDFDict(), "DocMDP", pdf.NewPDFDict(), "UR3", pdf.NewPDFDict())
		ref = kv("Type", nm("SigRef"), "DigestMethod", nm("MD5"), "DigestValue", pdf.PDFHexString{Value: "AB"})
		sig := kv("Type", nm("Sig"), "Reference", pdf.PDFArray{ref, pdf.PDFInteger(1)})
		other := kv("Reference", pdf.PDFArray{kv("DigestMethod", nm("MD5"))})
		noRefs := kv("Type", nm("Sig"))
		root := kv("Perms", perms, "Things", pdf.PDFArray{sig, other, noRefs})
		return kv("Root", root), perms, ref
	}
	f := permissionsFixer{}
	st := pdf.Checks.Structure

	trailer, perms, ref := build()
	if changed, _ := f.Fix(&trailer, issuesOf(st.PermissionsKeys)); !changed {
		t.Error("PermissionsKeys: no change")
	}
	if perms.Entries.Get("UR") != nil || perms.Entries.Len() != 2 {
		t.Errorf("permissions = %v, want UR3 and DocMDP", perms.Entries)
	}
	if ref.Entries.Get("DigestMethod") == nil {
		t.Error("PermissionsKeys alone removed a signature reference digest")
	}

	trailer, perms, ref = build()
	if changed, _ := f.Fix(&trailer, issuesOf(st.SignatureReferenceDigest)); !changed {
		t.Error("SignatureReferenceDigest: no change")
	}
	if ref.Entries.Get("DigestMethod") != nil || ref.Entries.Get("DigestValue") != nil || ref.Entries.Get("Type") == nil {
		t.Errorf("signature reference = %v", ref.Entries)
	}
	if perms.Entries.Get("UR") == nil {
		t.Error("SignatureReferenceDigest alone removed a permission")
	}
	if changed, _ := f.Fix(&trailer, issuesOf(st.SignatureReferenceDigest)); changed {
		t.Error("a second Fix changed the graph")
	}

	empty := kv("Root", pdf.NewPDFDict())
	if changed, _ := f.Fix(&empty, issuesOf(st.PermissionsKeys)); changed {
		t.Error("Fix changed a document without permissions")
	}
	if !f.Applies(st.PermissionsKeys) || !f.Applies(st.SignatureReferenceDigest) || f.Applies(st.NameNotUTF8) {
		t.Error("Applies covers the wrong checks")
	}
}

func TestPageBoundaryFixer(t *testing.T) {
	box := func(llx, lly, urx, ury float64) pdf.PDFArray { return boxArray([4]float64{llx, lly, urx, ury}) }
	for _, tc := range []struct {
		desc   string
		parent pdf.PDFDict // the Pages node's own entries
		page   pdf.PDFDict
		want   map[string]pdf.PDFValue // nil means the key is absent
		lost   bool
	}{
		{
			desc: "every box too small",
			page: kv("MediaBox", box(0, 0, 2, 2), "CropBox", box(0, 0, 2, 2), "BleedBox", box(0, 0, 2, 2),
				"TrimBox", box(0, 0, 2, 2), "ArtBox", box(0, 0, 4, 4)),
			want: map[string]pdf.PDFValue{"MediaBox": box(0, 0, 3, 3), "CropBox": box(0, 0, 3, 3),
				"BleedBox": nil, "TrimBox": nil, "ArtBox": box(0, 0, 4, 4)},
		},
		{
			desc: "boxes larger than the MediaBox",
			page: kv("MediaBox", box(0, 0, 500, 500), "CropBox", box(0, 0, 14405, 14405), "TrimBox", box(0, 0, 14405, 14405)),
			want: map[string]pdf.PDFValue{"CropBox": box(0, 0, 500, 500), "TrimBox": box(0, 0, 500, 500)},
		},
		{
			desc: "MediaBox too large around a small CropBox",
			page: kv("MediaBox", box(0, 0, 14402, 14402), "CropBox", box(10, 10, 14, 14)),
			want: map[string]pdf.PDFValue{"MediaBox": box(10, 10, 14, 14), "CropBox": box(10, 10, 14, 14)},
		},
		{
			desc: "MediaBox too large on one axis",
			page: kv("MediaBox", box(0, 0, 500, 20000)),
			want: map[string]pdf.PDFValue{"MediaBox": box(0, 0, 500, 14400)},
			lost: true,
		},
		{
			desc: "CropBox too small and outside the MediaBox",
			page: kv("MediaBox", box(0, 0, 500, 500), "CropBox", box(600, 600, 602, 602)),
			want: map[string]pdf.PDFValue{"CropBox": box(0, 0, 500, 500)},
		},
		{
			desc:   "inherited CropBox too small",
			parent: kv("CropBox", box(0, 0, 2, 2)),
			page:   kv("MediaBox", box(0, 0, 500, 500)),
			want:   map[string]pdf.PDFValue{"CropBox": box(0, 0, 500, 500)},
		},
		{
			desc:   "inherited MediaBox too small",
			parent: kv("MediaBox", box(0, 0, 2, 2)),
			page:   kv("CropBox", pdf.PDFArray{pdf.PDFInteger(1)}),
			want:   map[string]pdf.PDFValue{"MediaBox": box(0, 0, 3, 3)},
		},
		{
			desc: "fractional box",
			page: kv("MediaBox", box(0.5, 0, 2, 500)),
			want: map[string]pdf.PDFValue{"MediaBox": pdf.PDFArray{pdf.PDFReal(0.5), pdf.PDFInteger(0), pdf.PDFReal(3.5), pdf.PDFInteger(500)}},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tc.page.Entries.Set("Type", nm("Page"))
			pages := kv("Type", nm("Pages"), "Kids", pdf.PDFArray{tc.page}, "Count", pdf.PDFInteger(1))
			for k, v := range tc.parent.Entries.All() {
				pages.Entries.Set(k, v)
			}
			tc.page.Entries.Set("Parent", pages)
			trailer := kv("Root", kv("Type", nm("Catalog"), "Pages", pages))
			var lost []pdf.PDFError
			f := pageBoundaryFixer{spec: a2bSpec, lost: &lost}
			if changed, err := f.Fix(&trailer, nil); err != nil || !changed {
				t.Fatalf("Fix = %v, %v, want a change", changed, err)
			}
			for k, want := range tc.want {
				got := tc.page.Entries.Get(k)
				if want == nil && got != nil || want != nil && !pdf.EqualPDFValue(got, want) {
					t.Errorf("%s = %v, want %v", k, got, want)
				}
			}
			if (len(lost) > 0) != tc.lost {
				t.Errorf("lost = %v, want lost content: %v", lost, tc.lost)
			}
			if changed, _ := f.Fix(&trailer, nil); changed {
				t.Error("a second Fix changed the graph")
			}
		})
	}
}

func TestPageBoundaryFixerSkips(t *testing.T) {
	noPages := kv("Root", kv("Type", nm("Catalog")))
	if changed, _ := (pageBoundaryFixer{spec: a2bSpec}).Fix(&noPages, nil); changed {
		t.Error("Fix changed a document without pages")
	}
	page := kv("Type", nm("Page"), "MediaBox", boxArray([4]float64{0, 0, 1, 1}))
	trailer := kv("Root", kv("Pages", kv("Type", nm("Pages"), "Kids", pdf.PDFArray{page})))
	if changed, _ := (pageBoundaryFixer{spec: a1bSpec}).Fix(&trailer, nil); changed {
		t.Error("Fix changed a page under a level without page boundary limits")
	}
	if !(pageBoundaryFixer{}).Applies(pdf.Checks.Structure.PageBoundarySize) {
		t.Error("pageBoundaryFixer does not apply to PageBoundarySize")
	}
}

func TestStreamFilterFixer(t *testing.T) {
	st := pdf.Checks.Structure
	flate := func() pdf.PDFDict {
		d := pdf.NewPDFDict()
		writer.SetStreamFlate(&d, []byte("hello"))
		return d
	}
	for _, tc := range []struct {
		desc    string
		stream  func() pdf.PDFDict
		checks  []pdf.Check
		changed bool
		filter  pdf.PDFValue // the Filter after the fix
		image   bool         // whether the chain ends in an image codec
	}{
		{
			desc:    "abbreviated name",
			stream:  func() pdf.PDFDict { d := flate(); d.Entries.Set("Filter", nm("Fl")); return d },
			checks:  []pdf.Check{st.StreamFilterNotAllowed},
			changed: true,
			filter:  nm("FlateDecode"),
		},
		{
			desc: "abbreviated names in an array",
			stream: func() pdf.PDFDict {
				d := flate()
				d.Entries.Set("Filter", pdf.PDFArray{nm("Fl"), pdf.PDFInteger(1), nm("DCTDecode")})
				return d
			},
			checks:  []pdf.Check{st.StreamFilterNotAllowed},
			changed: true,
			filter:  pdf.PDFArray{nm("FlateDecode"), pdf.PDFInteger(1), nm("DCTDecode")},
			image:   true,
		},
		{
			desc: "decodable chain with a non-Identity Crypt filter left as it is",
			stream: func() pdf.PDFDict {
				d := flate()
				d.Entries.Set("Filter", nm("Crypt"))
				d.Entries.Set("DecodeParms", kv("Name", nm("StdCF")))
				return d
			},
			checks: []pdf.Check{st.StreamFilterNotAllowed},
			filter: nm("Crypt"),
		},
		{
			desc: "undecodable unknown filter",
			stream: func() pdf.PDFDict {
				d := flate()
				d.Entries.Set("Filter", nm("BrotliDecode"))
				return d
			},
			checks: []pdf.Check{st.StreamFilterNotAllowed},
			filter: nm("BrotliDecode"),
		},
		{
			desc: "abbreviation in a one-element array",
			stream: func() pdf.PDFDict {
				d := pdf.NewStreamDict(nil, []byte("68656c6c6f>"))
				d.Entries.Set("Filter", pdf.PDFArray{nm("AHx")})
				d.Entries.Set("DecodeParms", pdf.PDFArray{kv("Name", nm("X"))})
				return d
			},
			checks:  []pdf.Check{st.StreamFilterNotAllowed},
			changed: true,
			filter:  pdf.PDFArray{nm("ASCIIHexDecode")},
		},
		{
			desc: "LZW as a filter outside the level's set",
			stream: func() pdf.PDFDict {
				d := pdf.NewStreamDict(nil, encodeLZW(t, []byte("hello")))
				d.Entries.Set("Filter", nm("LZWDecode"))
				return d
			},
			checks:  []pdf.Check{st.StreamFilterNotAllowed},
			changed: true,
			filter:  nm("FlateDecode"),
		},
		{
			desc:   "allowed chain",
			stream: flate,
			checks: []pdf.Check{st.StreamFilterNotAllowed},
			filter: nm("FlateDecode"),
		},
		{
			desc:   "not reported",
			stream: func() pdf.PDFDict { d := flate(); d.Entries.Set("Filter", nm("Fl")); return d },
			checks: []pdf.Check{st.StreamLZWFilter},
			filter: nm("Fl"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			s := tc.stream()
			trailer := kv("Root", kv("Things", pdf.PDFArray{s, kv("Plain", nm("x"))}))
			changed, err := streamFilterFixer{spec: a2bSpec}.Fix(&trailer, issuesOf(tc.checks...))
			if err != nil || changed != tc.changed {
				t.Fatalf("Fix = %v, %v, want %v", changed, err, tc.changed)
			}
			if got := s.Entries.Get("Filter"); !pdf.EqualPDFValue(got, tc.filter) {
				t.Errorf("Filter = %v, want %v", got, tc.filter)
			}
			if data, err := pdf.DecodeStream(s); tc.changed && !tc.image && (err != nil || string(data) != "hello") {
				t.Errorf("decoded = %q, %v", data, err)
			}
		})
	}
}

// TestStreamFilterFixerDisallowed verifies which chains the fixer counts as
// outside the level's filters.
func TestStreamFilterFixerDisallowed(t *testing.T) {
	f := streamFilterFixer{spec: a2bSpec}
	for _, tc := range []struct {
		filter pdf.PDFValue
		parms  pdf.PDFValue
		want   bool
	}{
		{nm("FlateDecode"), nil, false},
		{nm("LZWDecode"), nil, true},
		{nm("Crypt"), nil, false},
		{nm("Crypt"), kv("Name", nm("Identity")), false},
		{nm("Crypt"), kv("Name", nm("StdCF")), true},
	} {
		d := pdf.NewStreamDict(nil, nil)
		d.Entries.Set("Filter", tc.filter)
		if tc.parms != nil {
			d.Entries.Set("DecodeParms", tc.parms)
		}
		if got := f.disallowed(d); got != tc.want {
			t.Errorf("disallowed(%v, %v) = %v, want %v", tc.filter, tc.parms, got, tc.want)
		}
	}
}

func TestContentLimitsFixerZeroesTinyReals(t *testing.T) {
	st := pdf.Checks.Structure
	tiny := pdf.PDFReal(1e-39)
	for _, tc := range []struct {
		checks []pdf.Check
		want   pdf.PDFReal
	}{
		{[]pdf.Check{st.RealTooCloseToZero}, 0},
		{[]pdf.Check{st.RealOutOfRange}, tiny},
	} {
		holder := kv("V", tiny, "W", pdf.PDFReal(0.5))
		trailer := kv("Root", holder)
		if _, err := (contentLimitsFixer{spec: a2bSpec}).Fix(&trailer, issuesOf(tc.checks...)); err != nil {
			t.Fatal(err)
		}
		if got := holder.Entries.Get("V"); got != tc.want {
			t.Errorf("%v: V = %v, want %v", tc.checks, got, tc.want)
		}
		if got := holder.Entries.Get("W"); got != pdf.PDFReal(0.5) {
			t.Errorf("%v: W = %v, want 0.5", tc.checks, got)
		}
	}
	if !(contentLimitsFixer{}).Applies(st.RealTooCloseToZero) {
		t.Error("contentLimitsFixer does not apply to RealTooCloseToZero")
	}
}
