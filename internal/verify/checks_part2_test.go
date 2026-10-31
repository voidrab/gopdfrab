package verify

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// kv builds a dictionary from alternating keys and values.
func kv(pairs ...any) pdf.PDFDict {
	d := pdf.NewPDFDict()
	for i := 0; i < len(pairs); i += 2 {
		d.Entries.Set(pairs[i].(string), pairs[i+1].(pdf.PDFValue))
	}
	return d
}

func name(s string) pdf.PDFName { return pdf.PDFName{Value: s} }

// a2bContext returns a context for the PDF/A-2b spec.
func a2bContext() *ValidationContext {
	spec := a2b()
	return &ValidationContext{Spec: &spec}
}

func TestValidateUnicodeNames(t *testing.T) {
	parent := kv("Type", name("StructTreeRoot"))
	for _, tc := range []struct {
		desc string
		dict pdf.PDFDict
		want bool
	}{
		{"valid font name", kv("Type", name("Font"), "BaseFont", name("Caf#C3#A9")), false},
		{"font name not UTF-8", kv("Type", name("Font"), "BaseFont", name("Caf#E9")), true},
		{"font name not a name", kv("Type", name("Font"), "BaseFont", pdf.PDFInteger(1)), false},
		{"role map key not UTF-8", kv("Type", name("StructTreeRoot"), "RoleMap", kv("P#E9", name("P"))), true},
		{"role map value not UTF-8", kv("Type", name("StructTreeRoot"), "RoleMap", kv("Para", name("P#E9"))), true},
		{"valid role map", kv("Type", name("StructTreeRoot"), "RoleMap", kv("Para", name("P"))), false},
		{"struct tree root without role map", kv("Type", name("StructTreeRoot")), false},
		{"typed structure element", kv("Type", name("StructElem"), "S", name("P#E9")), true},
		{"untyped structure element", kv("S", name("P#E9"), "P", parent), true},
		{"untyped dictionary without a parent", kv("S", name("P#E9")), false},
		{"untyped dictionary whose S is no name", kv("S", pdf.PDFInteger(1), "P", parent), false},
		{"other type", kv("Type", name("Annot"), "S", name("P#E9"), "P", parent), false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			validateUnicodeNames(tc.dict, ctx)
			if got := hasCheck(ctx, pdf.Checks.Structure.NameNotUTF8); got != tc.want {
				t.Errorf("NameNotUTF8 reported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidateColorantNames(t *testing.T) {
	alt, fn := name("DeviceCMYK"), pdf.NewPDFDict()
	for _, tc := range []struct {
		desc string
		cs   pdf.PDFArray
		want bool
	}{
		{"Separation", pdf.PDFArray{name("Separation"), name("Cyan#C2"), alt, fn}, true},
		{"valid Separation", pdf.PDFArray{name("Separation"), name("Cyan"), alt, fn}, false},
		{"DeviceN", pdf.PDFArray{name("DeviceN"), pdf.PDFArray{name("Cyan"), name("Spot#FF")}, alt, fn}, true},
		{"valid DeviceN", pdf.PDFArray{name("DeviceN"), pdf.PDFArray{name("Cyan"), pdf.PDFInteger(1)}, alt, fn}, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			validateColourSpaceArray(tc.cs, ctx)
			if got := hasCheck(ctx, pdf.Checks.Structure.NameNotUTF8); got != tc.want {
				t.Errorf("NameNotUTF8 reported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPermissionsRule(t *testing.T) {
	trailer := func(perms pdf.PDFValue) pdf.PDFValue {
		root := kv("Type", name("Catalog"))
		if perms != nil {
			root.Entries.Set("Perms", perms)
		}
		return kv("Root", root)
	}
	for _, tc := range []struct {
		desc  string
		graph pdf.PDFValue
		want  bool
	}{
		{"no permissions", trailer(nil), false},
		{"permissions not a dictionary", trailer(pdf.PDFInteger(1)), false},
		{"UR3 and DocMDP", trailer(kv("UR3", pdf.NewPDFDict(), "DocMDP", pdf.NewPDFDict())), false},
		{"another key", trailer(kv("UR", pdf.NewPDFDict(), "DocMDP", pdf.NewPDFDict())), true},
		{"graph not a dictionary", pdf.PDFInteger(1), false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			errs := permissionsRule(nil, tc.graph, a2bContext())
			if got := len(errs) == 1 && errs[0].Check() == pdf.Checks.Structure.PermissionsKeys; got != tc.want || len(errs) > 1 {
				t.Errorf("permissionsRule = %v, want a PermissionsKeys issue: %v", errs, tc.want)
			}
		})
	}
}

func TestValidateSignatureReferences(t *testing.T) {
	withDocMDP := func(has bool) *ValidationContext {
		ctx := a2bContext()
		ctx.docMDP = &has
		return ctx
	}
	sig := func(refs ...pdf.PDFValue) pdf.PDFDict {
		return kv("Type", name("Sig"), "Reference", pdf.PDFArray(refs))
	}
	digest := kv("Type", name("SigRef"), "DigestMethod", name("MD5"))
	for _, tc := range []struct {
		desc   string
		dict   pdf.PDFDict
		docMDP bool
		want   bool
	}{
		{"digest with DocMDP", sig(digest), true, true},
		{"digest without DocMDP", sig(digest), false, false},
		{"no digest", sig(kv("Type", name("SigRef"))), true, false},
		{"reference not a dictionary", sig(pdf.PDFInteger(1)), true, false},
		{"untyped signature with a byte range", kv("ByteRange", pdf.PDFArray{}, "Reference", pdf.PDFArray{digest}), true, true},
		{"not a signature", kv("Reference", pdf.PDFArray{digest}), true, false},
		{"no references", kv("Type", name("Sig")), true, false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := withDocMDP(tc.docMDP)
			validateSignatureReferences(tc.dict, ctx)
			if got := hasCheck(ctx, pdf.Checks.Structure.SignatureReferenceDigest); got != tc.want {
				t.Errorf("SignatureReferenceDigest reported = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHasDocMDP(t *testing.T) {
	if (&ValidationContext{}).hasDocMDP() {
		t.Error("a context without a reader has DocMDP permissions")
	}
	for _, tc := range []struct {
		desc  string
		perms pdf.PDFDict
		want  bool
	}{
		{"DocMDP", kv("DocMDP", pdf.NewPDFDict()), true},
		{"UR3 only", kv("UR3", pdf.NewPDFDict()), false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := &ValidationContext{reader: rawReader("", kv("Root", kv("Perms", tc.perms)))}
			if got := ctx.hasDocMDP(); got != tc.want {
				t.Errorf("hasDocMDP = %v, want %v", got, tc.want)
			}
			// The answer is cached.
			ctx.reader = nil
			if got := ctx.hasDocMDP(); got != tc.want {
				t.Errorf("cached hasDocMDP = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestValidatePageBoundaries(t *testing.T) {
	box := func(w, h float64) pdf.PDFArray {
		return pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFReal(w), pdf.PDFReal(h)}
	}
	page := func(pairs ...any) pdf.PDFDict { return kv(append([]any{"Type", name("Page")}, pairs...)...) }
	for _, tc := range []struct {
		desc string
		dict pdf.PDFDict
		want bool
	}{
		{"valid", page("MediaBox", box(612, 792), "CropBox", box(500, 500)), false},
		{"MediaBox too small", page("MediaBox", box(2, 792)), true},
		{"MediaBox too large", page("MediaBox", box(612, 14401)), true},
		{"limits inclusive", page("MediaBox", box(3, 14400)), false},
		{"inverted box", page("MediaBox", pdf.PDFArray{pdf.PDFInteger(612), pdf.PDFInteger(792), pdf.PDFInteger(0), pdf.PDFInteger(0)}), false},
		{"inherited MediaBox", page("Parent", kv("MediaBox", box(2, 2))), true},
		{"inherited CropBox", page("MediaBox", box(612, 792), "Parent", kv("Parent", kv("CropBox", box(2, 2)))), true},
		{"ArtBox", page("MediaBox", box(612, 792), "ArtBox", box(1, 1)), true},
		{"ArtBox not inherited", page("MediaBox", box(612, 792), "Parent", kv("ArtBox", box(1, 1))), false},
		{"malformed box", page("MediaBox", pdf.PDFArray{pdf.PDFInteger(0)}), false},
		{"no boxes", page(), false},
		{"not a page", kv("MediaBox", box(1, 1)), false},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := a2bContext()
			validatePageBoundaries(tc.dict, ctx)
			if got := hasCheck(ctx, pdf.Checks.Structure.PageBoundarySize); got != tc.want {
				t.Errorf("PageBoundarySize reported = %v, want %v", got, tc.want)
			}
		})
	}

	// A level without page boundary limits checks none.
	ctx := &ValidationContext{Spec: a1bSpec}
	validatePageBoundaries(page("MediaBox", box(1, 1)), ctx)
	if len(ctx.errs) != 0 {
		t.Errorf("PDF/A-1b reported %v", ctx.errs)
	}
}

// TestInheritedPageAttributeStopsAtTheDepthCap verifies that a Parent chain
// deeper than pdf.MaxPageTreeDepth is not followed to its end.
func TestInheritedPageAttributeStopsAtTheDepthCap(t *testing.T) {
	top := kv("CropBox", pdf.PDFArray{})
	node := top
	for range pdf.MaxPageTreeDepth + 1 {
		node = kv("Parent", node)
	}
	if v := inheritedPageAttribute(node, "CropBox"); v != nil {
		t.Errorf("inheritedPageAttribute = %v past the depth cap", v)
	}
	if v := inheritedPageAttribute(kv("Parent", top), "CropBox"); v == nil {
		t.Error("inheritedPageAttribute missed the parent's CropBox")
	}
}
