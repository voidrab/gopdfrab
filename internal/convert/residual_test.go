package convert

import (
	"testing"

	"github.com/voidrab/gopdfrab/internal/check"
)

func TestResidualCategory(t *testing.T) {
	tests := []struct {
		check check.Check
		want  string
	}{
		{check.Checks.Font.SubsetGlyphCoverage, "font"},
		{check.Checks.Font.SimpleNotEmbedded, "font"},
		{check.Checks.Structure.StringTooLong, "content-stream"},
		// Not classified: either genuinely novel, fixable by a future
		// dictionary-level fixup that doesn't exist yet (e.g.
		// CIDToGIDMapMissing), or already fully handled by a registered
		// fixer (InlineImageLZWFilter -- now incl. the predictor case,
		// fixups_inline_image.go; UndefinedOperator -- fixups_content.go;
		// the Transparency checks -- fixups_transparency.go; DeviceNColorants
		// -- fixups_devicen.go).
		{check.Checks.Structure.InlineImageLZWFilter, ""},
		{check.Checks.Font.CIDToGIDMapMissing, ""},
		{check.Checks.Action.ForbiddenActionType, ""},
		{check.Checks.Colour.UndefinedOperator, ""},
		{check.Checks.Transparency.ImageWithSoftMask, ""},
		{check.Checks.Transparency.TransparencyGroup, ""},
		{check.Checks.Structure.DeviceNColorants, ""},
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
