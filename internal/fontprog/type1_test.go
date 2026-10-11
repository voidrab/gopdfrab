package fontprog

import (
	"testing"
)

func TestParseType1AdvanceWidth(t *testing.T) {
	// sbw: sbx sby wx wy -> advance is the 3rd operand.
	sbw := []byte{139, 139, byte(139 + 10), 139, 12, 8} // 0,0,10,0, escape 12 8 (sbw)
	if w, ok := parseType1AdvanceWidth(sbw, nil); !ok || w != 10 {
		t.Errorf("parseType1AdvanceWidth(sbw, nil) = %v, %v, want 10, true", w, ok)
	}
	if _, ok := parseType1AdvanceWidth([]byte{14}, nil); ok {
		t.Error("parseType1AdvanceWidth(endchar only) should be ok=false")
	}
	if _, ok := parseType1AdvanceWidth(nil, nil); ok {
		t.Error("parseType1AdvanceWidth(empty) should be ok=false")
	}
}

func TestParseType1AdvanceWidthMoreOperators(t *testing.T) {
	// Negative number via the 251-254 range, then hsbw.
	cs := []byte{byte(251), 0, 139, 13} // push -108, push 0, hsbw -> wx = stack[1] = 0
	if w, ok := parseType1AdvanceWidth(cs, nil); !ok || w != 0 {
		t.Errorf("parseType1AdvanceWidth(251-254 range) = %v, %v", w, ok)
	}

	// 2-byte integer (op 28) then hsbw.
	cs28 := []byte{28, 0x01, 0x2C, 139, 13} // push 300, push 0, hsbw -> wx=0
	if w, ok := parseType1AdvanceWidth(cs28, nil); !ok || w != 0 {
		t.Errorf("parseType1AdvanceWidth(op28) = %v, %v", w, ok)
	}

	// 4-byte integer (op 29) then hsbw, using the pushed value as wx.
	cs29 := []byte{139, 29, 0x00, 0x00, 0x01, 0x2C, 13} // push 0 (sbx), push 300 (wx), hsbw
	if w, ok := parseType1AdvanceWidth(cs29, nil); !ok || w != 300 {
		t.Errorf("parseType1AdvanceWidth(op29) = %v, %v", w, ok)
	}

	// A non-hsbw/sbw/endchar operator clears the stack and parsing continues.
	csClear := []byte{139, 9, 139, byte(139 + 5), 13} // push 0, closepath(9,clears), push 0, push 5, hsbw
	if w, ok := parseType1AdvanceWidth(csClear, nil); !ok || w != 5 {
		t.Errorf("parseType1AdvanceWidth(stack-clear op) = %v, %v", w, ok)
	}

	// Truncated operand encodings should fail cleanly rather than panic.
	for _, bad := range [][]byte{
		{247},      // 247-250 missing 2nd byte
		{251},      // 251-254 missing 2nd byte
		{28, 0},    // op28 missing 3rd byte
		{29, 0, 0}, // op29 missing bytes
		{12},       // escape missing 2nd byte
	} {
		if _, ok := parseType1AdvanceWidth(bad, nil); ok {
			t.Errorf("parseType1AdvanceWidth(%v) should fail on truncated input", bad)
		}
	}
}

func TestSplitCharSetNames(t *testing.T) {
	got := SplitCharSetNames("/a /b/c  ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("SplitCharSetNames = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("SplitCharSetNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
