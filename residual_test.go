package pdfrab

import "testing"

func TestResidualCategory(t *testing.T) {
	tests := []struct {
		check Check
		want  string
	}{
		{Checks.Font.SubsetGlyphCoverage, "font"},
		{Checks.Font.SimpleNotEmbedded, "font"},
		{Checks.Structure.InlineImageLZWFilter, "content-stream"},
		{Checks.Structure.StringTooLong, "content-stream"},
		// Not classified: either genuinely novel, fixable by a future
		// dictionary-level fixup that doesn't exist yet (e.g.
		// CIDToGIDMapMissing), or already fully handled by a registered
		// fixer (UndefinedOperator -- fixups_content.go; the Transparency
		// checks -- fixups_transparency.go).
		{Checks.Font.CIDToGIDMapMissing, ""},
		{Checks.Action.ForbiddenActionType, ""},
		{Checks.Colour.UndefinedOperator, ""},
		{Checks.Transparency.ImageWithSoftMask, ""},
		{Checks.Transparency.TransparencyGroup, ""},
	}
	for _, tt := range tests {
		got := ResidualCategory(tt.check)
		if tt.want == "" {
			if got != "" {
				t.Errorf("ResidualCategory(%s) = %q, want unclassified", tt.check.Name(), got)
			}
			continue
		}
		if got == "" || got[:len(tt.want)] != tt.want {
			t.Errorf("ResidualCategory(%s) = %q, want prefix %q", tt.check.Name(), got, tt.want)
		}
	}
}
