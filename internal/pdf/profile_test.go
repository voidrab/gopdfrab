package pdf

import (
	"strings"
	"testing"
)

func TestProfileString(t *testing.T) {
	p := NewProfile(A_1B)
	s := p.String()
	if !strings.Contains(s, "Profile{") || !strings.Contains(s, "enabled:") {
		t.Errorf("Profile.String() = %q", s)
	}
}
