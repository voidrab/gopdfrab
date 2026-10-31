package verify

import (
	"fmt"
	"math"
	"slices"
	"unicode/utf8"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// 6.1.8 Name objects (ISO 19005-2)

// checkUnicodeName reports name, if it is one, when its bytes are not valid
// UTF-8 once its #XX escapes are expanded. what names the kind of name in the
// report.
func checkUnicodeName(name pdf.PDFValue, owner pdf.PDFValue, what string, ctx *ValidationContext) {
	n, ok := name.(pdf.PDFName)
	if !ok || utf8.Valid(pdf.DecodePDFName(n.Value)) {
		return
	}
	ctx.Report(pdf.Checks.Structure.NameNotUTF8, owner, fmt.Sprintf("%s /%s is not valid UTF-8", what, n.Value))
}

// validateUnicodeNames checks the font names, structure type names and role
// map names a dictionary holds. Colorant names are checked with their colour
// space array.
func validateUnicodeNames(v pdf.PDFDict, ctx *ValidationContext) {
	switch v.Entries.Get("Type") {
	case pdf.PDFName{Value: "Font"}:
		checkUnicodeName(v.Entries.Get("BaseFont"), v, "font name", ctx)
	case pdf.PDFName{Value: "StructTreeRoot"}:
		roleMap, _ := v.Entries.Get("RoleMap").(pdf.PDFDict)
		for k, mapped := range roleMap.Entries.All() {
			checkUnicodeName(pdf.PDFName{Value: k}, v, "role map structure type", ctx)
			checkUnicodeName(mapped, v, "role map structure type", ctx)
		}
	}
	if isStructElem(v) {
		checkUnicodeName(v.Entries.Get("S"), v, "structure type", ctx)
	}
}

// isStructElem reports whether v is a structure element: a dictionary of
// type StructElem, or one without a type that has a structure type and a
// parent.
func isStructElem(v pdf.PDFDict) bool {
	switch v.Entries.Get("Type") {
	case pdf.PDFName{Value: "StructElem"}:
		return true
	case nil:
		_, hasS := v.Entries.Get("S").(pdf.PDFName)
		_, hasP := v.Entries.Get("P").(pdf.PDFDict)
		return hasS && hasP
	}
	return false
}

// validateColorantNames checks the colorant names of a Separation or DeviceN
// colour space array.
func validateColorantNames(arr pdf.PDFArray, ctx *ValidationContext) {
	switch arr[0] {
	case pdf.PDFName{Value: "Separation"}:
		checkUnicodeName(arr[1], arr, "Separation colorant", ctx)
	case pdf.PDFName{Value: "DeviceN"}:
		names, _ := arr[1].(pdf.PDFArray)
		for _, n := range names {
			checkUnicodeName(n, arr, "DeviceN colorant", ctx)
		}
	}
}

// 6.1.12 Permissions (ISO 19005-2)

// permittedPermissions are the only keys a permissions dictionary may hold.
var permittedPermissions = []string{"DocMDP", "UR3"}

// signatureDigestKeys are the keys a signature reference must not hold when
// the document has DocMDP permissions.
var signatureDigestKeys = []string{"DigestLocation", "DigestMethod", "DigestValue"}

// permissionsRule checks the keys of the catalog's permissions dictionary.
func permissionsRule(_ *pdf.Reader, graph pdf.PDFValue, _ *ValidationContext) []pdf.PDFError {
	root := graphCatalog(graph)
	perms, ok := root.Entries.Get("Perms").(pdf.PDFDict)
	if !ok {
		return nil
	}
	var extra []string
	for k := range perms.Entries.All() {
		if !slices.Contains(permittedPermissions, k) {
			extra = append(extra, k)
		}
	}
	if len(extra) == 0 {
		return nil
	}
	slices.Sort(extra)
	return []pdf.PDFError{pdf.NewError(
		pdf.Checks.Structure.PermissionsKeys,
		[]error{fmt.Errorf("permissions dictionary contains keys other than UR3 and DocMDP: %v", extra)},
		0,
		refOf(root),
	)}
}

// graphCatalog returns the catalog of a resolved trailer, or an empty
// dictionary.
func graphCatalog(graph pdf.PDFValue) pdf.PDFDict {
	trailer, _ := graph.(pdf.PDFDict)
	root, _ := trailer.Entries.Get("Root").(pdf.PDFDict)
	return root
}

// validateSignatureReferences checks the signature references of a signature
// dictionary in a document with DocMDP permissions.
func validateSignatureReferences(v pdf.PDFDict, ctx *ValidationContext) {
	refs, ok := v.Entries.Get("Reference").(pdf.PDFArray)
	if !ok || v.Entries.Get("Type") != (pdf.PDFName{Value: "Sig"}) && v.Entries.Get("ByteRange") == nil {
		return
	}
	if !ctx.hasDocMDP() {
		return
	}
	for _, r := range refs {
		ref, ok := r.(pdf.PDFDict)
		if !ok {
			continue
		}
		for _, k := range signatureDigestKeys {
			if ref.Entries.Get(k) != nil {
				ctx.Report(pdf.Checks.Structure.SignatureReferenceDigest, v,
					fmt.Sprintf("signature reference contains %s although the document has DocMDP permissions", k))
			}
		}
	}
}

// 6.1.13 Implementation limits (ISO 19005-2)

// pageBoundaries are the page boundaries ISO 32000-1 14.11.2 defines. A page
// inherits its MediaBox and CropBox from the page tree.
var pageBoundaries = []struct {
	key       string
	inherited bool
}{
	{"MediaBox", true},
	{"CropBox", true},
	{"BleedBox", false},
	{"TrimBox", false},
	{"ArtBox", false},
}

// validatePageBoundaries checks the size of each boundary of a page.
func validatePageBoundaries(v pdf.PDFDict, ctx *ValidationContext) {
	lo, hi := ctx.Spec.Limits.MinPageBoundary, ctx.Spec.Limits.MaxPageBoundary
	if hi == 0 || v.Entries.Get("Type") != (pdf.PDFName{Value: "Page"}) {
		return
	}
	for _, b := range pageBoundaries {
		box := v.Entries.Get(b.key)
		if box == nil && b.inherited {
			box = inheritedPageAttribute(v, b.key)
		}
		r, err := pdf.FloatArray(box)
		if err != nil || len(r) != 4 {
			continue
		}
		w, h := math.Abs(r[2]-r[0]), math.Abs(r[3]-r[1])
		if w < lo || w > hi || h < lo || h > hi {
			ctx.Report(pdf.Checks.Structure.PageBoundarySize, v, fmt.Sprintf(
				"page %s is %g x %g units; each side must be %g to %g", b.key, w, h, lo, hi))
		}
	}
}

// inheritedPageAttribute returns the value of key from the nearest ancestor
// of a page in the page tree, or nil.
func inheritedPageAttribute(page pdf.PDFDict, key string) pdf.PDFValue {
	node := page
	for range pdf.MaxPageTreeDepth {
		parent, ok := node.Entries.Get("Parent").(pdf.PDFDict)
		if !ok {
			return nil
		}
		if v := parent.Entries.Get(key); v != nil {
			return v
		}
		node = parent
	}
	return nil
}
