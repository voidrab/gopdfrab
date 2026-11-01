package convert

import (
	"github.com/voidrab/gopdfrab/internal/fontprog"
	"github.com/voidrab/gopdfrab/internal/pdf"
	"github.com/voidrab/gopdfrab/internal/verify"
	"github.com/voidrab/gopdfrab/internal/writer"
)

// notdefFixer remediates Checks.Font.NotdefGlyphReference (6.2.11.8). It
// removes from the strings that content streams show each character code that
// selects the .notdef glyph. Each removed code leaves a TJ adjustment of its
// advance, so the text after it keeps its position; only the .notdef glyph
// itself, an empty box or nothing, is lost. A ' or " operator whose string
// loses a code is rewritten as the operators it stands for. A code cut short
// at the end of a string is dropped.
//
// The font state of a stream starts empty, so text shown before the stream's
// own Tf is left alone. That includes text a form shows in a font selected by
// the stream that draws it.
type notdefFixer struct{}

func (notdefFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Font.NotdefGlyphReference
}

func (notdefFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	vctx := &verify.ValidationContext{}
	// One entry per font dictionary for the whole walk, so that a font many
	// streams use has its program read once.
	fonts := map[uintptr]*notdefFont{}
	return walkContentStreamDictsWithResources(trailer, func(stream, resources pdf.PDFDict) (pdf.PDFDict, bool) {
		return stripNotdefCodes(stream, resources, fonts, vctx)
	}), nil
}

// notdefTextState is the part of the text state that decides how far a
// shown code advances: the font and its size, and the character and word
// spacing.
type notdefTextState struct {
	font         *notdefFont
	size, tc, tw float64
}

// notdefFont is a font the stream selects, with what removing a code needs.
type notdefFont struct {
	codeLen int
	notdef  func(code int) bool
	width   func(code int) float64
}

// stripNotdefCodes rewrites the strings stream shows so that none selects
// .notdef, and reports whether it changed anything.
func stripNotdefCodes(stream, resources pdf.PDFDict, cache map[uintptr]*notdefFont, vctx *verify.ValidationContext) (pdf.PDFDict, bool) {
	data, err := pdf.DecodeStream(stream)
	if err != nil {
		return stream, false
	}
	fonts, _ := resources.Entries.Get("Font").(pdf.PDFDict)
	fontNamed := func(name string) *notdefFont {
		font, ok := fonts.Entries.Get(name).(pdf.PDFDict)
		if !ok {
			return nil
		}
		ptr := pdf.ValuePointer(font.Entries)
		if f, ok := cache[ptr]; ok {
			return f
		}
		var nf *notdefFont
		if codeLen, notdef, ok := verify.NotdefSelector(font, vctx); ok {
			nf = &notdefFont{codeLen: codeLen, notdef: notdef, width: glyphWidthFunc(font)}
		}
		cache[ptr] = nf
		return nf
	}

	var cw writer.ContentStreamWriter
	modified := false
	var st notdefTextState
	var stack []notdefTextState
	cs := pdf.NewContentScanner(data)
	cs.Scan(func(op string, operands []pdf.PDFValue) {
		switch op {
		case "q":
			stack = append(stack, st)
		case "Q":
			if n := len(stack); n > 0 {
				st, stack = stack[n-1], stack[:n-1]
			}
		case "Tf":
			if len(operands) == 2 {
				name, _ := operands[0].(pdf.PDFName)
				size, _ := pdf.PDFNumberToFloat(operands[1])
				st.font, st.size = fontNamed(name.Value), size
			}
		case "Tc":
			if len(operands) == 1 {
				st.tc, _ = pdf.PDFNumberToFloat(operands[0])
			}
		case "Tw":
			if len(operands) == 1 {
				st.tw, _ = pdf.PDFNumberToFloat(operands[0])
			}
		case "Tj", "'", "\"", "TJ":
			// The " operator sets the word and character spacing whether or
			// not its string is rewritten.
			if op == "\"" && len(operands) == 3 {
				st.tw, _ = pdf.PDFNumberToFloat(operands[0])
				st.tc, _ = pdf.PDFNumberToFloat(operands[1])
			}
			if ops, ok := st.stripShown(op, operands); ok {
				for _, o := range ops {
					_ = cw.WriteOp(o.Op, o.Operands)
				}
				modified = true
				return
			}
		}
		_ = cw.WriteOp(op, operands)
	})
	if !modified || !cs.Complete() || cw.Err() != nil {
		return stream, false
	}
	writer.SetStreamFlate(&stream, cw.Bytes())
	return stream, true
}

// stripShown returns the operators that show what a text-showing operator
// shows less its .notdef codes, and false when it shows none.
func (st *notdefTextState) stripShown(op string, operands []pdf.PDFValue) ([]writer.ContentOp, bool) {
	if st.font == nil || len(operands) == 0 {
		return nil, false
	}
	var prefix []writer.ContentOp
	switch op {
	case "\"":
		if len(operands) != 3 {
			return nil, false
		}
		prefix = []writer.ContentOp{
			{Op: "Tw", Operands: operands[:1]},
			{Op: "Tc", Operands: operands[1:2]},
			{Op: "T*"},
		}
	case "'":
		prefix = []writer.ContentOp{{Op: "T*"}}
	}

	var items pdf.PDFArray
	if op == "TJ" {
		items, _ = operands[0].(pdf.PDFArray)
	} else {
		items = pdf.PDFArray{operands[len(operands)-1]}
	}
	out := make(pdf.PDFArray, 0, len(items))
	stripped := false
	for _, item := range items {
		raw, isString := shownBytes(item)
		if !isString {
			out = append(out, item)
			continue
		}
		parts, ok := st.stripString(raw)
		if !ok {
			out = append(out, item)
			continue
		}
		out = append(out, parts...)
		stripped = true
	}
	if !stripped {
		return nil, false
	}
	return append(prefix, writer.ContentOp{Op: "TJ", Operands: []pdf.PDFValue{out}}), true
}

// stripString splits a shown string around its .notdef codes into strings
// and the adjustments that stand in for the removed codes' advances, and
// reports false when it has no such code.
func (st *notdefTextState) stripString(raw []byte) (pdf.PDFArray, bool) {
	f := st.font
	var parts pdf.PDFArray
	var kept []byte
	stripped := false
	flush := func() {
		if len(kept) > 0 {
			parts = append(parts, pdf.PDFString{Value: string(kept)})
			kept = nil
		}
	}
	for i := 0; i < len(raw); i += f.codeLen {
		if i+f.codeLen > len(raw) {
			stripped = true // a code cut short selects no glyph
			break
		}
		code := 0
		for _, b := range raw[i : i+f.codeLen] {
			code = code<<8 | int(b)
		}
		if !f.notdef(code) {
			kept = append(kept, raw[i:i+f.codeLen]...)
			continue
		}
		stripped = true
		flush()
		if adj := st.advance(code); adj != 0 {
			parts = append(parts, pdf.PDFReal(-adj))
		}
	}
	if !stripped {
		return nil, false
	}
	flush()
	return parts, true
}

// advance returns how far showing code moves the text position, in the
// thousandths of a text space unit that a TJ adjustment counts in: its width
// plus the character spacing, and the word spacing for a single-byte code 32.
func (st *notdefTextState) advance(code int) float64 {
	adv := st.font.width(code)
	if st.size != 0 {
		spacing := st.tc
		if st.font.codeLen == 1 && code == 32 {
			spacing += st.tw
		}
		adv += spacing * 1000 / st.size
	}
	return adv
}

// shownBytes returns the bytes a string operand shows, and false for an
// operand that is no string.
func shownBytes(v pdf.PDFValue) ([]byte, bool) {
	switch s := v.(type) {
	case pdf.PDFString:
		return []byte(s.Value), true
	case pdf.PDFHexString:
		return pdf.DecodePDFHexStringBytes(s.Value), true
	}
	return nil, false
}

// glyphWidthFunc returns the width a font dictionary gives each code, in
// thousandths of text space: a simple font's Widths entry or MissingWidth,
// or a composite font's W entry or DW. A Type 3 font's widths are in glyph
// space, which its FontMatrix maps to text space.
func glyphWidthFunc(font pdf.PDFDict) func(code int) float64 {
	if (font.Entries.Get("Subtype") == pdf.PDFName{Value: "Type0"}) {
		cid := verify.DescendantCIDFont(font)
		widths := map[int]int{}
		w, _ := cid.Entries.Get("W").(pdf.PDFArray)
		for _, pair := range fontprog.ParseCIDWidths(w) {
			widths[pair[0]] = pair[1]
		}
		dw := 1000.0
		if v, ok := pdf.PDFNumberToFloat(cid.Entries.Get("DW")); ok {
			dw = v
		}
		return func(c int) float64 {
			if w, ok := widths[c]; ok {
				return float64(w)
			}
			return dw
		}
	}
	first, _ := font.Entries.Get("FirstChar").(pdf.PDFInteger)
	widths, _ := font.Entries.Get("Widths").(pdf.PDFArray)
	desc, _ := font.Entries.Get("FontDescriptor").(pdf.PDFDict)
	missing, _ := pdf.PDFNumberToFloat(desc.Entries.Get("MissingWidth"))
	scale := 1.0 // a FontMatrix of [0.001 0 0 0.001 0 0] maps to thousandths
	if (font.Entries.Get("Subtype") == pdf.PDFName{Value: "Type3"}) {
		if m, ok := font.Entries.Get("FontMatrix").(pdf.PDFArray); ok && len(m) == 6 {
			if a, ok := pdf.PDFNumberToFloat(m[0]); ok {
				scale = 1000 * a
			}
		}
	}
	return func(code int) float64 {
		if i := code - int(first); i >= 0 && i < len(widths) {
			if w, ok := pdf.PDFNumberToFloat(widths[i]); ok {
				return w * scale
			}
		}
		return missing * scale
	}
}
