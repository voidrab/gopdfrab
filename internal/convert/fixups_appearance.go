package convert

import (
	"encoding/hex"
	"strings"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/writer"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// This file registers a Fixer for the appearance-stream checks classified
// as family B (resource synthesis) in the converter plan: a missing /AP on
// an annotation or form-field widget (6.9, 6.5.3), and a malformed /AP
// (extra entries, missing N, or N of the wrong shape, 6.5.3). It rebuilds
// /AP from scratch as "<< /N <value> >>", synthesizing a minimal Form
// XObject -- with rendered text for text/choice field values, using the
// bundled appearanceFont() (fixups_appearance_font.go) -- wherever no
// already-valid N value can be kept as-is.

func init() {
	registerFixer(appearanceFixer{})
}

// appearanceFixer remediates WidgetMissingAppearance, MissingAppearance,
// AppearanceMissingN, AppearanceExtraEntries and AppearanceNNotStream,
// mirroring the /AP block of validateAnnotation (checks_dict.go).
type appearanceFixer struct{}

func (appearanceFixer) Applies(c pdf.Check) bool {
	switch c {
	case pdf.Checks.Form.WidgetMissingAppearance, pdf.Checks.Annotation.MissingAppearance,
		pdf.Checks.Annotation.AppearanceMissingN, pdf.Checks.Annotation.AppearanceExtraEntries,
		pdf.Checks.Annotation.AppearanceNNotStream:
		return true
	}
	return false
}

func (appearanceFixer) Fix(trailer *pdf.PDFDict, issues []pdf.PDFError) (bool, error) {
	changed := false
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if (d.Entries["Type"] != pdf.PDFName{Value: "Annot"}) {
			return
		}
		subtype, _ := d.Entries["Subtype"].(pdf.PDFName)
		if !verify.AllowedAnnotationTypes[subtype.Value] {
			return
		}
		if !annotationNeedsAppearanceFix(d) {
			return
		}
		d.Entries["AP"] = rebuiltAppearanceDict(trailer, d)
		changed = true
	})
	return changed, nil
}

// annotationNeedsAppearanceFix reports whether d violates one of this
// Fixer's checks, exactly mirroring validateAnnotation's /AP block
// (checks_dict.go) so the Fixer only ever touches what that check would
// actually flag.
func annotationNeedsAppearanceFix(d pdf.PDFDict) bool {
	subtype, _ := d.Entries["Subtype"].(pdf.PDFName)
	ap, hasAP := d.Entries["AP"].(pdf.PDFDict)
	if !hasAP {
		return subtype.Value != "Popup" && subtype.Value != "Link"
	}

	n, hasN := ap.Entries["N"]
	if !hasN {
		return true
	}
	for k := range ap.Entries {
		if k != "N" && k != "_ref" {
			return true
		}
	}

	nd, ok := n.(pdf.PDFDict)
	if !ok {
		return true
	}
	if isBtnField(d) {
		return nd.HasStream
	}
	return !nd.HasStream
}

// isBtnField reports whether d is a Btn form field, resolving FT through
// the Parent chain if not set directly on the widget.
func isBtnField(d pdf.PDFDict) bool {
	for depth := 0; depth < 20; depth++ {
		if ft, ok := d.Entries["FT"].(pdf.PDFName); ok {
			return ft.Value == "Btn"
		}
		parent, ok := d.Entries["Parent"].(pdf.PDFDict)
		if !ok {
			return false
		}
		d = parent
	}
	return false
}

// rebuiltAppearanceDict returns a conformant replacement for d's /AP: a
// dictionary containing only N. An already-valid N value (per
// annotationNeedsAppearanceFix's shape rule) is kept as-is -- e.g. when the
// only violation was an extra /D or /R entry -- rather than discarding a
// perfectly good appearance just to strip the other keys.
func rebuiltAppearanceDict(trailer *pdf.PDFDict, d pdf.PDFDict) pdf.PDFDict {
	isBtn := isBtnField(d)
	var newN pdf.PDFValue

	if ap, ok := d.Entries["AP"].(pdf.PDFDict); ok {
		if n, ok := ap.Entries["N"]; ok {
			if nd, ok := n.(pdf.PDFDict); ok {
				switch {
				case isBtn && !nd.HasStream:
					newN = nd
				case !isBtn && nd.HasStream:
					newN = nd
				case isBtn && nd.HasStream:
					newN = pdf.PDFDict{Entries: map[string]pdf.PDFValue{buttonState(d): nd}}
				}
			}
		}
	}

	if newN == nil {
		box := buildAppearanceXObject(trailer, d, isBtn)
		if isBtn {
			newN = pdf.PDFDict{Entries: map[string]pdf.PDFValue{buttonState(d): box}}
		} else {
			newN = box
		}
	}
	return pdf.PDFDict{Entries: map[string]pdf.PDFValue{"N": newN}}
}

// buttonState returns d's current /AS appearance-state name, defaulting to
// (and recording) "Off" if absent -- a button's /N subdictionary needs some
// state key, and /AS should name one of them.
func buttonState(d pdf.PDFDict) string {
	if as, ok := d.Entries["AS"].(pdf.PDFName); ok && as.Value != "" {
		return as.Value
	}
	d.Entries["AS"] = pdf.PDFName{Value: "Off"}
	return "Off"
}

// buildAppearanceXObject synthesizes a minimal Form XObject sized to d's
// /Rect: rendered text for a text/choice field's current value, or an empty
// (but structurally valid) appearance otherwise -- buttons render no text
// here since their caption belongs on the state stream, not the box itself.
func buildAppearanceXObject(trailer *pdf.PDFDict, d pdf.PDFDict, isBtn bool) pdf.PDFDict {
	w, h := annotBBox(d)

	xobj := pdf.NewPDFDict()
	xobj.Entries["Type"] = pdf.PDFName{Value: "XObject"}
	xobj.Entries["Subtype"] = pdf.PDFName{Value: "Form"}
	xobj.Entries["FormType"] = pdf.PDFInteger(1)
	xobj.Entries["BBox"] = pdf.PDFArray{pdf.PDFInteger(0), pdf.PDFInteger(0), pdf.PDFReal(float32(w)), pdf.PDFReal(float32(h))}

	var content []byte
	resources := pdf.NewPDFDict()
	if !isBtn && isTextLikeField(d) {
		if text := fieldDisplayText(d); text != "" {
			content, resources = buildTextAppearanceContent(trailer, d, text, w, h)
		}
	}

	xobj.Entries["Resources"] = resources
	if err := writer.SetStreamFlate(&xobj, content); err != nil {
		return xobj
	}
	return xobj
}

func isTextLikeField(d pdf.PDFDict) bool {
	ft, _ := climbField(d, "FT")
	name, ok := ft.(pdf.PDFName)
	return ok && (name.Value == "Tx" || name.Value == "Ch")
}

// annotBBox returns the width/height of d's /Rect, or 0,0 if absent or
// malformed -- a zero-area BBox is still a structurally valid Form XObject.
func annotBBox(d pdf.PDFDict) (w, h float64) {
	arr, ok := d.Entries["Rect"].(pdf.PDFArray)
	if !ok || len(arr) != 4 {
		return 0, 0
	}
	var v [4]float64
	for i, e := range arr {
		f, ok := verify.AsFloat(e)
		if !ok {
			return 0, 0
		}
		v[i] = f
	}
	return verify.Abs64(v[2] - v[0]), verify.Abs64(v[3] - v[1])
}

// climbField looks up key on d, falling back to its /Parent chain (cycle
// guarded) -- AcroForm fields may merge into a single Widget+Field
// dictionary, or split inheritable keys onto an ancestor Field dictionary
// shared by several Kids widgets.
func climbField(d pdf.PDFDict, key string) (pdf.PDFValue, bool) {
	visited := map[uintptr]bool{}
	for {
		if v, ok := d.Entries[key]; ok {
			return v, true
		}
		parent, ok := d.Entries["Parent"].(pdf.PDFDict)
		if !ok {
			return nil, false
		}
		ptr := pdf.ValuePointer(parent.Entries)
		if visited[ptr] {
			return nil, false
		}
		visited[ptr] = true
		d = parent
	}
}

// fieldDisplayText returns the single-line text to render for d's current
// value (/V, possibly inherited), decoding a hex string as UTF-16BE when
// BOM-marked (the PDF text-string convention) and otherwise treating string
// bytes as already WinAnsi-compatible.
func fieldDisplayText(d pdf.PDFDict) string {
	v, ok := climbField(d, "V")
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case pdf.PDFString:
		return sanitizeSingleLine(t.Value)
	case pdf.PDFHexString:
		raw, err := hex.DecodeString(t.Value)
		if err != nil || len(raw) == 0 {
			return ""
		}
		if len(raw) >= 2 && raw[0] == 0xFE && raw[1] == 0xFF {
			return sanitizeSingleLine(decodeUTF16BEToWinAnsi(raw[2:]))
		}
		return sanitizeSingleLine(string(raw))
	}
	return ""
}

var (
	unicodeToWinAnsiOnce sync.Once
	unicodeToWinAnsiMap  map[uint16]byte
)

// winAnsiForUnicode returns the WinAnsiEncoding code for a Unicode code
// point, built lazily from verify.WinAnsiToUnicode (checks_font_program.go) on
// first use rather than as a package-level var initializer, since Go
// initializes package-level vars before running any init() func -- the one
// that actually populates verify.WinAnsiToUnicode would not have run yet.
func winAnsiForUnicode(u uint16) (byte, bool) {
	unicodeToWinAnsiOnce.Do(func() {
		unicodeToWinAnsiMap = make(map[uint16]byte, 224)
		for cc := 0x20; cc <= 0xFF; cc++ {
			if uc := verify.WinAnsiToUnicode[cc]; uc != 0 {
				if _, exists := unicodeToWinAnsiMap[uc]; !exists {
					unicodeToWinAnsiMap[uc] = byte(cc)
				}
			}
		}
	})
	b, ok := unicodeToWinAnsiMap[u]
	return b, ok
}

// decodeUTF16BEToWinAnsi converts UTF-16BE bytes to WinAnsiEncoding,
// dropping any code point (e.g. a surrogate pair) outside WinAnsi's range --
// best-effort, since the alternative is a font program with no glyph for it.
func decodeUTF16BEToWinAnsi(b []byte) string {
	var sb strings.Builder
	for i := 0; i+1 < len(b); i += 2 {
		u := uint16(b[i])<<8 | uint16(b[i+1])
		if c, ok := winAnsiForUnicode(u); ok {
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// sanitizeSingleLine collapses line breaks and tabs to spaces, since v1
// appearance synthesis only renders a single line.
func sanitizeSingleLine(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\r', '\n', '\t':
			sb.WriteByte(' ')
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// escapeLiteralString backslash-escapes the characters that delimit or
// introduce escapes in a PDF literal string, since the bytes are about to
// be written verbatim between "(" and ")" (writer.go's writeOperand applies
// no escaping of its own).
func escapeLiteralString(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\', '(', ')':
			sb.WriteByte('\\')
			sb.WriteByte(c)
		default:
			sb.WriteByte(c)
		}
	}
	return sb.String()
}

// parseDA extracts the font size (Tf's second operand) and non-stroking
// colour operator from a /DA default-appearance string; the font name
// itself is ignored since the synthesized appearance always uses
// appearanceFont().
func parseDA(da string) (size float64, colorOps []writer.ContentOp) {
	pdf.NewContentScanner([]byte(da)).Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "Tf":
			if len(operands) == 2 {
				if f, ok := verify.AsFloat(operands[1]); ok {
					size = f
				}
			}
		case "g", "rg", "k":
			colorOps = append(colorOps, writer.ContentOp{Op: op, Operands: append([]pdf.PDFValue{}, operands...)})
		}
	})
	return size, colorOps
}

// formLevelDA returns the AcroForm's default /DA string, the fallback a
// field uses when it specifies no /DA of its own.
func formLevelDA(trailer *pdf.PDFDict) string {
	root, ok := trailer.Entries["Root"].(pdf.PDFDict)
	if !ok {
		return ""
	}
	form, ok := root.Entries["AcroForm"].(pdf.PDFDict)
	if !ok {
		return ""
	}
	da, ok := form.Entries["DA"].(pdf.PDFString)
	if !ok {
		return ""
	}
	return da.Value
}

// buildTextAppearanceContent renders text as a single left-aligned,
// vertically-centered line clipped to the BBox, using size/colour parsed
// from the field's effective /DA (or the AcroForm's, or a fallback).
func buildTextAppearanceContent(trailer *pdf.PDFDict, d pdf.PDFDict, text string, w, h float64) ([]byte, pdf.PDFDict) {
	daStr := ""
	if da, ok := climbField(d, "DA"); ok {
		if s, ok := da.(pdf.PDFString); ok {
			daStr = s.Value
		}
	}
	if daStr == "" {
		daStr = formLevelDA(trailer)
	}
	size, colorOps := parseDA(daStr)
	if size <= 0 || size > h {
		size = h * 0.6
	}
	if size <= 0 {
		size = 10
	}
	if len(colorOps) == 0 {
		colorOps = []writer.ContentOp{{Op: "g", Operands: []pdf.PDFValue{pdf.PDFInteger(0)}}}
	}

	x, y := 2.0, (h-size)/2
	if y < 1 {
		y = 1
	}

	ops := []writer.ContentOp{
		{Op: "q"},
		{Op: "re", Operands: []pdf.PDFValue{pdf.PDFReal(0), pdf.PDFReal(0), pdf.PDFReal(float32(w)), pdf.PDFReal(float32(h))}},
		{Op: "W"},
		{Op: "n"},
		{Op: "BT"},
		{Op: "Tf", Operands: []pdf.PDFValue{pdf.PDFName{Value: "F0"}, pdf.PDFReal(float32(size))}},
	}
	ops = append(ops, colorOps...)
	ops = append(ops,
		writer.ContentOp{Op: "Td", Operands: []pdf.PDFValue{pdf.PDFReal(float32(x)), pdf.PDFReal(float32(y))}},
		writer.ContentOp{Op: "Tj", Operands: []pdf.PDFValue{pdf.PDFString{Value: escapeLiteralString(text)}}},
		writer.ContentOp{Op: "ET"},
		writer.ContentOp{Op: "Q"},
	)

	content, err := writer.WriteContentStream(ops)
	if err != nil {
		return nil, pdf.NewPDFDict()
	}

	resources := pdf.NewPDFDict()
	fontRes := pdf.NewPDFDict()
	fontRes.Entries["F0"] = appearanceFont()
	resources.Entries["Font"] = fontRes
	return content, resources
}
