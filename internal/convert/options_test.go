package convert

import "testing"

func TestOptionsDefaults(t *testing.T) {
	var zero Options
	if got := zero.iterations(); got != defaultMaxIterations {
		t.Errorf("zero.iterations() = %d, want default %d", got, defaultMaxIterations)
	}
	if got := zero.dpi(); got != defaultRasterDPI {
		t.Errorf("zero.dpi() = %d, want default %d", got, defaultRasterDPI)
	}

	set := Options{MaxIterations: 9, RasterDPI: 300}
	if got := set.iterations(); got != 9 {
		t.Errorf("iterations() = %d, want 9", got)
	}
	if got := set.dpi(); got != 300 {
		t.Errorf("dpi() = %d, want 300", got)
	}
}

// TestTransparencyFlattenerDPIFallback pins that a zero-dpi flattener (the
// registry prototype) still renders at the default resolution rather than a
// 0x0 image.
func TestTransparencyFlattenerDPIFallback(t *testing.T) {
	if got := (transparencyFlattener{}).renderDPI(); got != defaultRasterDPI {
		t.Errorf("renderDPI() = %d, want default %d", got, defaultRasterDPI)
	}
	if got := (transparencyFlattener{dpi: 200}).renderDPI(); got != 200 {
		t.Errorf("renderDPI() = %d, want 200", got)
	}
}
