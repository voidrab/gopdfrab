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
			desc: "visible area too large on one axis",
			page: kv("MediaBox", box(0, 0, 500, 20000)),
			want: map[string]pdf.PDFValue{"MediaBox": box(0, 0, 250, 10000), "UserUnit": pdf.PDFReal(2)},
		},
		{
			desc: "visible area too large inside a larger MediaBox",
			page: kv("MediaBox", box(0, 0, 60000, 60000), "CropBox", box(0, 0, 30000, 500), "UserUnit", pdf.PDFReal(1.5)),
			want: map[string]pdf.PDFValue{"MediaBox": box(0, 0, 7500, 125), "CropBox": box(0, 0, 7500, 125),
				"UserUnit": pdf.PDFReal(6)},
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
			f := pageBoundaryFixer{spec: a2bSpec}
			if changed, err := f.Fix(&trailer, nil); err != nil || !changed {
				t.Fatalf("Fix = %v, %v, want a change", changed, err)
			}
			for k, want := range tc.want {
				got := tc.page.Entries.Get(k)
				if want == nil && got != nil || want != nil && !pdf.EqualPDFValue(got, want) {
					t.Errorf("%s = %v, want %v", k, got, want)
				}
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

// TestPageBoundaryFixerScalesThePage verifies that a page scaled through its
// user unit (/UserUnit) keeps its content, annotations, beads and destinations
// in place relative to its boundaries.
func TestPageBoundaryFixerScalesThePage(t *testing.T) {
	box := func(llx, lly, urx, ury float64) pdf.PDFArray { return boxArray([4]float64{llx, lly, urx, ury}) }
	content := pdf.NewStreamDict(nil, []byte("0 0 m 20000 20000 l S"))
	annot := kv("Type", nm("Annot"), "Subtype", nm("Ink"), "Rect", box(100, 100, 200, 200),
		"InkList", pdf.PDFArray{pdf.PDFArray{pdf.PDFInteger(100), pdf.PDFInteger(100), nm("x")}})
	bead := kv("R", box(0, 0, 400, 400))
	page := kv("Type", nm("Page"), "Contents", content, "Annots", pdf.PDFArray{annot, pdf.PDFInteger(1)},
		"B", pdf.PDFArray{bead, pdf.PDFInteger(1)})
	other := kv("Type", nm("Page"), "MediaBox", box(0, 0, 612, 792), "Contents", pdf.PDFArray{content})
	pages := kv("Type", nm("Pages"), "Kids", pdf.PDFArray{page, other}, "Count", pdf.PDFInteger(2),
		"MediaBox", box(0, 0, 20000, 20000))
	page.Entries.Set("Parent", pages)
	other.Entries.Set("Parent", pages)
	dests := pdf.PDFArray{
		pdf.PDFArray{page, nm("XYZ"), pdf.PDFInteger(1000), pdf.PDFInteger(2000), nil},
		pdf.PDFArray{page, nm("FitR"), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(400), pdf.PDFInteger(400)},
		pdf.PDFArray{page, nm("FitH")},
		pdf.PDFArray{page, nm("Fit")},
		pdf.PDFArray{other, nm("XYZ"), pdf.PDFInteger(1000), pdf.PDFInteger(2000), nil},
		pdf.PDFArray{nm("XYZ"), nm("x")},
		pdf.PDFArray{pdf.PDFInteger(1)},
	}
	trailer := kv("Root", kv("Type", nm("Catalog"), "Pages", pages, "Dests", dests))

	if changed, err := (pageBoundaryFixer{spec: a2bSpec}).Fix(&trailer, nil); err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	for _, tc := range []struct {
		desc      string
		got, want pdf.PDFValue
	}{
		{"MediaBox", page.Entries.Get("MediaBox"), box(0, 0, 10000, 10000)},
		{"UserUnit", page.Entries.Get("UserUnit"), pdf.PDFReal(2)},
		{"annotation Rect", annot.Entries.Get("Rect"), box(50, 50, 100, 100)},
		{"annotation InkList", annot.Entries.Get("InkList"), pdf.PDFArray{pdf.PDFArray{pdf.PDFInteger(50), pdf.PDFInteger(50), nm("x")}}},
		{"bead", bead.Entries.Get("R"), box(0, 0, 200, 200)},
		// The page element is left out: comparing it would follow the
		// page tree's Parent cycle.
		{"XYZ destination", dests[0].(pdf.PDFArray)[1:], pdf.PDFArray{nm("XYZ"), pdf.PDFInteger(500), pdf.PDFInteger(1000), nil}},
		{"FitR destination", dests[1].(pdf.PDFArray)[1:], pdf.PDFArray{nm("FitR"), pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFInteger(200), pdf.PDFInteger(200)}},
		{"other page's destination", dests[4].(pdf.PDFArray)[1:], pdf.PDFArray{nm("XYZ"), pdf.PDFInteger(1000), pdf.PDFInteger(2000), nil}},
		{"other page", other.Entries.Get("UserUnit"), nil},
	} {
		if !pdf.EqualPDFValue(tc.got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.desc, tc.got, tc.want)
		}
	}
	contents, ok := page.Entries.Get("Contents").(pdf.PDFArray)
	if !ok || len(contents) != 3 {
		t.Fatalf("Contents = %v, want the content between a cm and a Q", page.Entries.Get("Contents"))
	}
	for i, want := range []string{"q\n0.5 0 0 0.5 0 0 cm\n", "0 0 m 20000 20000 l S", "Q\n"} {
		if data, _ := pdf.DecodeStream(contents[i].(pdf.PDFDict)); string(data) != want {
			t.Errorf("Contents[%d] = %q, want %q", i, data, want)
		}
	}
}

// TestPageBoundaryFixerScalesAnArrayOfContents verifies that a page whose
// Contents is an array keeps every stream, in order, inside the scaling.
func TestPageBoundaryFixerScalesAnArrayOfContents(t *testing.T) {
	a, b := pdf.NewStreamDict(nil, []byte("a")), pdf.NewStreamDict(nil, []byte("b"))
	page := kv("Type", nm("Page"), "MediaBox", boxArray([4]float64{0, 0, 30000, 100}), "Contents", pdf.PDFArray{a, b})
	trailer := kv("Root", kv("Pages", kv("Type", nm("Pages"), "Kids", pdf.PDFArray{page})))
	if _, err := (pageBoundaryFixer{spec: a2bSpec}).Fix(&trailer, nil); err != nil {
		t.Fatal(err)
	}
	contents, _ := page.Entries.Get("Contents").(pdf.PDFArray)
	if len(contents) != 4 || !pdf.EqualPDFValue(contents[1], a) || !pdf.EqualPDFValue(contents[2], b) {
		t.Errorf("Contents = %v", contents)
	}
	if got := page.Entries.Get("UserUnit"); got != pdf.PDFReal(4) {
		t.Errorf("UserUnit = %v, want 4", got)
	}
}

// hoistPage returns a trailer holding one page, which draws content with the
// given resources, and returns that page too.
func hoistPage(content string, resources pdf.PDFValue) (trailer, page pdf.PDFDict) {
	page = kv("Type", nm("Page"), "Contents", pdf.NewStreamDict(nil, []byte(content)))
	if resources != nil {
		page.Entries.Set("Resources", resources)
	}
	return kv("Root", kv("Type", nm("Catalog"), "Pages", kv("Type", nm("Pages"), "Kids", pdf.PDFArray{page}))), page
}

func TestInlineImageFilterFixerHoistsTheImage(t *testing.T) {
	cs := pdf.PDFArray{nm("ICCBased"), pdf.NewStreamDict(nil, nil)}
	resources := kv("ColorSpace", kv("CS0", cs), "XObject", kv("InlineIm1", pdf.NewStreamDict(nil, nil)))
	trailer, page := hoistPage("q 10 0 0 10 0 0 cm\nBI /W 1 /H 1 /CS /CS0 /BPC 8 /F [/AHx /JPXDecode] /DP [null << /Q 1 >>] /L 3 ID\nxyz\nEI\nQ\n", resources)

	changed, err := inlineImageFilterFixer{spec: a2bSpec}.Fix(&trailer, nil)
	if err != nil || !changed {
		t.Fatalf("Fix = %v, %v, want a change", changed, err)
	}
	content, _ := pdf.DecodeStream(page.Entries.Get("Contents").(pdf.PDFDict))
	if string(content) != "q\n10 0 0 10 0 0 cm\n/InlineIm2 Do\nQ\n" {
		t.Errorf("content = %q", content)
	}
	image, ok := resources.Entries.Get("XObject").(pdf.PDFDict).Entries.Get("InlineIm2").(pdf.PDFDict)
	if !ok {
		t.Fatal("no image XObject InlineIm2")
	}
	for _, tc := range []struct {
		key  string
		want pdf.PDFValue
	}{
		{"Type", nm("XObject")},
		{"Subtype", nm("Image")},
		{"Width", pdf.PDFInteger(1)},
		{"Height", pdf.PDFInteger(1)},
		{"BitsPerComponent", pdf.PDFInteger(8)},
		{"Filter", pdf.PDFArray{nm("ASCIIHexDecode"), nm("JPXDecode")}},
		{"DecodeParms", pdf.PDFArray{nil, kv("Q", pdf.PDFInteger(1))}},
		{"Length", nil},
	} {
		if got := image.Entries.Get(tc.key); !pdf.EqualPDFValue(got, tc.want) {
			t.Errorf("%s = %v, want %v", tc.key, got, tc.want)
		}
	}
	if got, ok := image.Entries.Get("ColorSpace").(pdf.PDFArray); !ok || len(got) != 2 || got[0] != (nm("ICCBased")) {
		t.Errorf("ColorSpace = %v, want the resource's colour space", image.Entries.Get("ColorSpace"))
	}
	if string(image.Stream()) != "xyz" {
		t.Errorf("image data = %q, want xyz", image.Stream())
	}
	if !(inlineImageFilterFixer{}).Applies(pdf.Checks.Structure.InlineImageFilterNotAllowed) {
		t.Error("inlineImageFilterFixer does not apply to InlineImageFilterNotAllowed")
	}
}

func TestInlineImageFilterFixerColourSpaces(t *testing.T) {
	h := inlineImageHoist{resources: kv("ColorSpace", kv("Named", nm("DeviceRGB")))}
	for _, tc := range []struct {
		in   pdf.PDFValue
		want pdf.PDFValue
		ok   bool
	}{
		{nm("G"), nm("DeviceGray"), true},
		{nm("DeviceCMYK"), nm("DeviceCMYK"), true},
		{nm("Named"), nm("DeviceRGB"), true},
		{nm("Missing"), nil, false},
		{pdf.PDFArray{nm("I"), nm("RGB"), pdf.PDFInteger(1), pdf.PDFString{Value: "abcdef"}},
			pdf.PDFArray{nm("Indexed"), nm("DeviceRGB"), pdf.PDFInteger(1), pdf.PDFString{Value: "abcdef"}}, true},
		{pdf.PDFArray{nm("Indexed"), nm("Missing")}, nil, false},
		{pdf.PDFArray{nm("I")}, nil, false},
		{pdf.PDFInteger(1), nil, false},
	} {
		got, ok := h.colourSpace(tc.in)
		if ok != tc.ok || ok && !pdf.EqualPDFValue(got, tc.want) {
			t.Errorf("colourSpace(%v) = %v, %v, want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestInlineImageFilterFixerLeavesOtherImages(t *testing.T) {
	for _, tc := range []struct {
		desc    string
		content string
	}{
		{"allowed filters", "BI /W 1 /H 1 /CS /G /BPC 8 /F /Fl ID\nxyz\nEI\n"},
		{"no filter", "BI /W 1 /H 1 /CS /G /BPC 8 ID\nx\nEI\n"},
		{"unknown filter", "BI /W 1 /H 1 /CS /G /BPC 8 /F [/JPXDecode /Brotli] ID\nxyz\nEI\n"},
		{"non-Identity Crypt", "BI /W 1 /H 1 /CS /G /BPC 8 /F /Crypt /DP << /Name /StdCF >> ID\nxyz\nEI\n"},
		{"unresolvable colour space", "BI /W 1 /H 1 /CS /Missing /BPC 8 /F /JPXDecode ID\nxyz\nEI\n"},
		{"other operators", "0 0 m 1 1 l S\n"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			trailer, _ := hoistPage(tc.content, nil)
			if changed, _ := (inlineImageFilterFixer{spec: a2bSpec}).Fix(&trailer, nil); changed {
				t.Error("Fix changed the content")
			}
		})
	}
	trailer, _ := hoistPage("BI /W 1 /H 1 /F /JPXDecode ID\nxyz\nEI\n", nil)
	if changed, _ := (inlineImageFilterFixer{spec: a1bSpec}).Fix(&trailer, nil); changed {
		t.Error("Fix changed the content under a level without inline image filter rules")
	}
}

// TestInlineImageFilterFixerGivesResources verifies that a page without
// resources gets them, once, for the images of all its content streams.
func TestInlineImageFilterFixerGivesResources(t *testing.T) {
	img := "BI /W 1 /H 1 /IM true /F /Crypt ID\nx\nEI\n"
	page := kv("Type", nm("Page"), "Contents", pdf.PDFArray{
		pdf.NewStreamDict(nil, []byte(img)), pdf.NewStreamDict(nil, []byte(img)),
	})
	trailer := kv("Root", kv("Pages", kv("Type", nm("Pages"), "Kids", pdf.PDFArray{page})))
	if changed, _ := (inlineImageFilterFixer{spec: a2bSpec}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix made no change")
	}
	res, _ := page.Entries.Get("Resources").(pdf.PDFDict)
	xobjects, _ := res.Entries.Get("XObject").(pdf.PDFDict)
	if xobjects.Entries.Len() != 2 {
		t.Errorf("XObject resources = %v, want both images", xobjects.Entries)
	}
}

// TestInlineImageHoistKeepsMalformedImages verifies that an inline image
// whose parameters are not key and value pairs is left as it is.
func TestInlineImageHoistKeepsMalformedImages(t *testing.T) {
	h := inlineImageHoist{spec: a2bSpec}
	changed := false
	operands := []pdf.PDFValue{pdf.PDFInteger(1), nm("JPXDecode"), pdf.InlineImageRaw{Data: []byte("x")}}
	if op, _ := h.rewrite("INLINEIMAGE", operands, &changed); op.Op != "INLINEIMAGE" || changed {
		t.Errorf("rewrite = %v, %v", op, changed)
	}
	if op, _ := h.rewrite("INLINEIMAGE", nil, &changed); op.Op != "INLINEIMAGE" || changed {
		t.Errorf("rewrite without operands = %v, %v", op, changed)
	}
}

// TestInlineImageFilterFixerHoistsFromAForm verifies that an inline image in
// a form XObject moves into the form's own resources.
func TestInlineImageFilterFixerHoistsFromAForm(t *testing.T) {
	form := pdf.NewStreamDict(nil, []byte("BI /W 1 /H 1 /CS /G /BPC 8 /F /JBIG2Decode ID\nx\nEI\n"))
	form.Entries.Set("Type", nm("XObject"))
	form.Entries.Set("Subtype", nm("Form"))
	trailer := kv("Root", kv("Things", pdf.PDFArray{form}))
	if changed, _ := (inlineImageFilterFixer{spec: a2bSpec}).Fix(&trailer, nil); !changed {
		t.Fatal("Fix made no change")
	}
	res, _ := form.Entries.Get("Resources").(pdf.PDFDict)
	xobjects, _ := res.Entries.Get("XObject").(pdf.PDFDict)
	image, _ := xobjects.Entries.Get("InlineIm1").(pdf.PDFDict)
	if image.Entries.Get("Filter") != (nm("JBIG2Decode")) {
		t.Errorf("form resources = %v", res.Entries)
	}
	if data, _ := pdf.DecodeStream(form); string(data) != "/InlineIm1 Do\n" {
		t.Errorf("form content = %q", data)
	}
}
