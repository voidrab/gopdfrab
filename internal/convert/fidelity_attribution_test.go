package convert

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// The fidelity survey says a page came out blank or came out drawn over. It
// does not say which repair did it, and a conversion runs a lot of them. This
// harness answers that: it runs one repair on its own over a file and measures
// the ink each page had before and after, so a cause is something measured
// rather than something ranked.
//
// The measurement is the survey's own -- the same renderer, the same ink
// fraction, the same thresholds -- with one difference worth knowing: this
// reads the graph in memory rather than the written file, so it sees what a
// repair did and not what the writer then made of it.

// inkMechanism is one repair, named for the report.
type inkMechanism struct {
	name string
	run  func(trailer *pdf.PDFDict, doc *pdf.Reader) error
}

// oneAtATime are the repairs that can move ink, in the order a conversion runs
// them. Each is measured from a fresh reading of the file, so nothing carries
// over from the one before.
var oneAtATime = []inkMechanism{
	{"preemptive", applyPreemptiveFixups},
	{"invisible-drawing", func(trailer *pdf.PDFDict, _ *pdf.Reader) error {
		dropInvisibleDrawing(trailer)
		return nil
	}},
	{"extgstate-dicts", func(trailer *pdf.PDFDict, _ *pdf.Reader) error {
		_, err := runDictVisitor(trailer, func(_ *pdf.PDFDict, changed *bool) (func(pdf.PDFDict), bool) {
			return extGStateDictVisitor(changed), true
		})
		return err
	}},
	{"transparency-flattener", func(trailer *pdf.PDFDict, _ *pdf.Reader) error {
		_, err := transparencyFlattener{}.Fix(trailer, nil)
		return err
	}},
}

// inkMechanisms is each repair on its own, and then all of them together --
// which is what a conversion does, short of writing the file out.
var inkMechanisms = append(slices.Clone(oneAtATime),
	inkMechanism{"all-of-them", func(trailer *pdf.PDFDict, doc *pdf.Reader) error {
		for _, m := range oneAtATime {
			if err := m.run(trailer, doc); err != nil {
				return err
			}
		}
		return nil
	}})

// TestFidelityAttribution measures what each repair does to the ink on a page.
// Off unless GOPDFRAB_FIDELITY_ATTRIBUTE names what to measure: a file, a
// directory of them, or a comma-separated list of either.
func TestFidelityAttribution(t *testing.T) {
	paths, problems := attributionPaths(os.Getenv("GOPDFRAB_FIDELITY_ATTRIBUTE"))
	for _, err := range problems {
		t.Error(err)
	}
	if len(paths) == 0 {
		t.Skip("set GOPDFRAB_FIDELITY_ATTRIBUTE to a file, a directory, or a comma-separated list")
	}
	attributeInk(t, paths)
}

// attributionPaths turns the setting into the files to measure: every PDF
// under a directory, or the file itself, sorted so two runs report the same
// order. What it could not make sense of comes back as problems.
func attributionPaths(setting string) (paths []string, problems []error) {
	for _, entry := range strings.Split(setting, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		info, err := os.Stat(entry)
		switch {
		case err != nil:
			problems = append(problems, err)
		case info.IsDir():
			err := filepath.WalkDir(entry, func(path string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".pdf") {
					paths = append(paths, path)
				}
				return nil
			})
			if err != nil {
				problems = append(problems, err)
			}
		default:
			paths = append(paths, entry)
		}
	}
	sort.Strings(paths)
	return paths, problems
}

// inkTally is what one repair did across the files measured.
type inkTally struct {
	blankedFiles, blankedPages         int
	overpaintedFiles, overpaintedPages int
}

// attributeInk reports, per repair, every page it empties and every page it
// draws over, and totals them at the end.
func attributeInk(t *testing.T, paths []string) map[string]*inkTally {
	t.Helper()
	tally := map[string]*inkTally{}
	for _, m := range inkMechanisms {
		tally[m.name] = &inkTally{}
	}

	for _, path := range paths {
		before, err := inkPerPage(path, nil)
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		name := filepath.Base(path)
		for _, m := range inkMechanisms {
			after, err := inkPerPage(path, m.run)
			if err != nil {
				t.Errorf("%s [%s]: %v", name, m.name, err)
				continue
			}
			blanked, overpainted := 0, 0
			for i := range min(len(before), len(after)) {
				page := PageFidelity{Page: i + 1, InputInk: before[i], OutputInk: after[i]}
				switch {
				case page.Blanked():
					blanked++
				case page.Overpainted():
					overpainted++
				default:
					continue
				}
				t.Logf("%s page %d [%s]: ink %.4f -> %.4f", name, page.Page, m.name, page.InputInk, page.OutputInk)
			}
			sum := tally[m.name]
			sum.blankedPages += blanked
			sum.overpaintedPages += overpainted
			if blanked > 0 {
				sum.blankedFiles++
			}
			if overpainted > 0 {
				sum.overpaintedFiles++
			}
		}
	}

	t.Logf("attribution over %d file(s):", len(paths))
	for _, m := range inkMechanisms {
		sum := tally[m.name]
		t.Logf("  %-22s blanked %d file(s) / %d page(s), drew over %d file(s) / %d page(s)",
			m.name, sum.blankedFiles, sum.blankedPages, sum.overpaintedFiles, sum.overpaintedPages)
	}
	return tally
}

// inkPerPage reads the file, runs one repair over it if given one, and returns
// how much ink each page carries afterwards.
func inkPerPage(path string, run func(*pdf.PDFDict, *pdf.Reader) error) ([]float64, error) {
	doc, err := pdf.Open(path)
	if err != nil {
		return nil, err
	}
	defer doc.Close()
	graph, err := doc.ResolveGraph()
	if err != nil {
		return nil, err
	}
	trailer, ok := graph.(pdf.PDFDict)
	if !ok {
		return nil, fmt.Errorf("resolved graph is not a dictionary")
	}
	if run != nil {
		if err := run(&trailer, doc); err != nil {
			return nil, err
		}
	}
	pages := renderTrailerPages(trailer, fidelityDPI)
	ink := make([]float64, len(pages))
	for i, page := range pages {
		ink[i] = inkFraction(page)
	}
	return ink, nil
}

// TestFidelityAttributionSelfCheck runs the harness where no corpus is needed,
// over the shape item 35 was written about: a page whose content is a black
// rectangle drawn at zero opacity. Making that opacity opaque is a repair that
// draws over the page, and naming which repair did it is the whole point of
// the harness -- so the answer is known in advance and can be asserted.
func TestFidelityAttributionSelfCheck(t *testing.T) {
	dir := t.TempDir()
	drawn := filepath.Join(dir, "zero-opacity.pdf")
	if err := os.WriteFile(drawn, onePageDocWithZeroAlpha(drawnAtZeroOpacity), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a pdf"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	missing := filepath.Join(dir, "gone.pdf")
	paths, problems := attributionPaths(dir + " , " + drawn + ",," + missing)
	if len(problems) != 1 {
		t.Errorf("problems = %v, want the one missing file", problems)
	}
	if want := []string{drawn, drawn}; !slices.Equal(paths, want) {
		t.Errorf("paths = %v, want the pdf twice (once per entry) and not the text file", paths)
	}

	tally := attributeInk(t, paths[:1])
	if got := tally["extgstate-dicts"]; got.overpaintedPages != 1 || got.overpaintedFiles != 1 {
		t.Errorf("making the opacity opaque drew over %+v, want one page of one file", got)
	}
	if got := tally["invisible-drawing"]; got.overpaintedPages != 0 || got.blankedPages != 0 {
		t.Errorf("taking the invisible drawing out changed %+v, want nothing", got)
	}
	if got := tally["all-of-them"]; got.overpaintedPages != 0 {
		t.Errorf("the repairs together drew over %+v, want nothing", got)
	}

	if _, err := inkPerPage(missing, nil); err == nil {
		t.Error("measuring a file that is not there should fail")
	}
}
