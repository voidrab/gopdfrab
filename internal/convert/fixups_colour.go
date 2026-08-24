package convert

import (
	_ "embed"
	"fmt"
	"runtime"
	"sync"

	"github.com/voidrab/gopdfrab/internal/pdf"

	"github.com/voidrab/gopdfrab/internal/verify"
)

// srgbICCProfile is the ICC's official sRGB v2 profile (color.org), embedded
// for any RGB OutputIntent/DefaultRGB colour space this package injects.
//
//go:embed assets/profiles/sRGB2014.icc
var srgbICCProfile []byte

// cmykICCProfile is a small-footprint FOGRA39 v2 CMYK profile, embedded for
// any CMYK OutputIntent/DefaultCMYK colour space this package injects. PDF/A-1
// requires ICC profiles no newer than v2.x (validateICCProfileStream); the
// "fogra39.icc" asset alongside it is v4 and therefore unusable here.
//
//go:embed assets/profiles/Small-footprint_FOGRA39v2.icc
var cmykICCProfile []byte

// grayICCProfile is the CC0 sGrey v2 profile, embedded so a one-component
// ICCBased colour space can be repaired in place rather than dropped for a
// device colour space. See NOTICE for where it comes from.
//
//go:embed assets/profiles/sGrey-v2-micro.icc
var grayICCProfile []byte

func init() {
	registerPreemptiveFixup(injectOutputIntent)
	registerFixer(iccBasedProfileFixer{})
	registerFixer(separationAlternateFixer{})
}

// colourModelN maps dominantColourModel's "rgb"/"cmyk" result to the /N an
// OutputIntent's ICC profile must declare to cover it. "gray" deliberately
// has no entry: it never needs a specific /N (see dominantColourModel).
var colourModelN = map[string]int{"rgb": 3, "cmyk": 4}

// injectOutputIntent ensures the document's catalog has a PDF/A OutputIntent
// backed by an embedded ICC profile.
func injectOutputIntent(trailer *pdf.PDFDict, doc *pdf.Reader) error {
	root, ok := trailer.Entries.Get("Root").(pdf.PDFDict)
	if !ok {
		return fmt.Errorf("injectOutputIntent: Root is not a dictionary")
	}

	dominant := dominantColourModel(detectColourModelUsage(*trailer, decoderFor(doc)))
	if existingN, ok := validPDFAOutputIntentN(root); ok {
		if dominant == "" || colourModelN[dominant] == existingN {
			return nil
		}
	}

	wantN, alternate, identifier, iccBytes := colourModelN["rgb"], "DeviceRGB", "sRGB", srgbICCProfile
	if dominant == "cmyk" {
		wantN, alternate, identifier, iccBytes = colourModelN["cmyk"], "DeviceCMYK", "FOGRA39", cmykICCProfile
	}

	profile := pdf.NewPDFDict()
	profile.Entries.Set("N", pdf.PDFInteger(wantN))
	profile.Entries.Set("Alternate", pdf.PDFName{Value: alternate})
	profile.HasStream = true
	profile.RawStream = iccBytes

	intent := pdf.NewPDFDict()
	intent.Entries.Set("Type", pdf.PDFName{Value: "OutputIntent"})
	intent.Entries.Set("S", pdf.PDFName{Value: "GTS_PDFA1"})
	intent.Entries.Set("OutputConditionIdentifier", pdf.PDFString{Value: identifier})
	intent.Entries.Set("Info", pdf.PDFString{Value: identifier + " ICC profile injected by gopdfrab"})
	intent.Entries.Set("DestOutputProfile", profile)
	root.Entries.Set("OutputIntents", pdf.PDFArray{intent})
	trailer.Entries.Set("Root", root)
	return nil
}

// separationAlternateFixer remediates 6.2.3.4: a Separation or DeviceN whose
// alternate space is a device space no OutputIntent covers. The alternate is
// swapped for an ICCBased space with the same number of components, so the
// tint transform feeding it needs no change and the colourant still resolves
// to the colour the document asked for -- where dropping the space or
// rasterizing the page would lose the colourant or the text on top of it.
type separationAlternateFixer struct{}

func (separationAlternateFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Colour.SeparationAlternateColour
}

func (separationAlternateFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	hasIntent, rgbCovered, cmykCovered := outputIntentCoverage(*trailer)
	covered := map[string]bool{"rgb": rgbCovered, "cmyk": cmykCovered, "gray": hasIntent}

	// One shared space per model, so the writer emits a single ICC stream
	// however many colourants the document defines.
	shared := map[string]pdf.PDFArray{}
	spaceFor := func(model string) (pdf.PDFArray, bool) {
		if cs, ok := shared[model]; ok {
			return cs, true
		}
		var cs pdf.PDFArray
		switch model {
		case "rgb":
			cs = iccBasedColourSpace(3, srgbICCProfile)
		case "cmyk":
			cs = iccBasedColourSpace(4, cmykICCProfile)
		case "gray":
			cs = iccBasedColourSpace(1, grayICCProfile)
		default:
			return nil, false
		}
		shared[model] = cs
		return cs, true
	}

	changed := false
	visit := func(v pdf.PDFValue) {
		arr, ok := v.(pdf.PDFArray)
		if !ok || len(arr) < 4 {
			return
		}
		head, ok := arr[0].(pdf.PDFName)
		if !ok || (head.Value != "Separation" && head.Value != "DeviceN") {
			return
		}
		model := verify.DeviceColourModel(arr[2])
		if model == "" || covered[model] {
			return
		}
		if cs, ok := spaceFor(model); ok {
			arr[2] = cs
			changed = true
		}
	}
	walkDicts(*trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		for k, v := range d.Entries.All() {
			if k == "_ref" {
				continue
			}
			visit(v)
			// A colour space also appears as the base of an Indexed space and
			// as an element of a /ColorSpace array elsewhere.
			if arr, ok := v.(pdf.PDFArray); ok {
				for _, item := range arr {
					visit(item)
				}
			}
		}
	})
	return changed, nil
}

// iccBasedColourSpace builds a "[/ICCBased <stream>]" colour-space array
// backed by profile (an embedded ICC v2 profile of n components), suitable
// for a /DefaultRGB or /DefaultCMYK resource entry.
func iccBasedColourSpace(n int, profile []byte) pdf.PDFArray {
	stream := pdf.NewPDFDict()
	stream.Entries.Set("N", pdf.PDFInteger(n))
	stream.HasStream = true
	stream.RawStream = profile
	return pdf.PDFArray{pdf.PDFName{Value: "ICCBased"}, stream}
}

// dominantColourModel returns "rgb" or "cmyk" based on which has the higher usage count,
// returning "" if neither is used. It ignores "gray" since any OutputIntent covers it,
// and checks keys in a fixed order to ensure tie-breakers are deterministic.
func dominantColourModel(usage map[string]int) string {
	best := ""
	for _, model := range [...]string{"rgb", "cmyk"} {
		if usage[model] > 0 && (best == "" || usage[model] > usage[best]) {
			best = model
		}
	}
	return best
}

// resourcesOf returns the resources dict names inside dict are looked up in:
// its own, or the ones handed down to it when it has none of its own.
func resourcesOf(dict, fallback pdf.PDFDict) pdf.PDFDict {
	if res, ok := dict.Entries.Get("Resources").(pdf.PDFDict); ok && res.Entries != nil {
		return res
	}
	return fallback
}

// decoderFor returns the run's shared decode function: the Reader's concurrent
// decoded-stream cache when doc is set, else the uncached pdf.DecodeStream.
func decoderFor(doc *pdf.Reader) decodeFunc {
	if doc != nil {
		return doc.DecodeStreamCachedConcurrent
	}
	return pdf.DecodeStream
}

// detectColourModelUsage counts how often RGB, Gray, and CMYK color models appear in the document graph.
// It checks dictionary-level color spaces everywhere, but only counts content-stream operators and inline
// images where they are actually used. decode supplies content-stream bytes (typically a cached path).
func detectColourModelUsage(trailer pdf.PDFDict, decode decodeFunc) map[string]int {
	counts := map[string]int{}

	var mu sync.Mutex
	scanVisited := map[uintptr]bool{}
	// claimScan returns false if ptr's content was already scanned, claiming it
	// otherwise.
	claimScan := func(ptr uintptr) bool {
		mu.Lock()
		defer mu.Unlock()
		if scanVisited[ptr] {
			return false
		}
		scanVisited[ptr] = true
		return true
	}

	scanContent := func(root, rootRes pdf.PDFDict, local map[string]int) {
		countModelExempt := func(model string, resources pdf.PDFDict) {
			if model == "" || verify.DefaultColorSpaceDefined(model, resources) {
				return
			}
			local[model]++
		}
		scanContentColour(root, rootRes, claimScan, decode, countModelExempt)
	}

	var jobs []scanJob
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if model := verify.DeviceColourModel(d.Entries.Get("ColorSpace")); model != "" {
			counts[model]++
		}

		for _, v := range d.Entries.All() {
			arr, ok := v.(pdf.PDFArray)
			if !ok || len(arr) < 3 {
				continue
			}
			head, ok := arr[0].(pdf.PDFName)
			if !ok || (head.Value != "Separation" && head.Value != "DeviceN") {
				continue
			}
			if model := verify.DeviceColourModel(arr[2]); model != "" {
				counts[model]++
			}
		}

		resources, _ := d.Entries.Get("Resources").(pdf.PDFDict)

		if pdf.EqualPDFValue(d.Entries.Get("Type"), pdf.PDFName{Value: "Page"}) {
			switch contents := d.Entries.Get("Contents").(type) {
			case pdf.PDFDict:
				if contents.HasStream {
					jobs = append(jobs, scanJob{contents, resources})
				}
			case pdf.PDFArray:
				for _, item := range contents {
					if cd, ok := item.(pdf.PDFDict); ok && cd.HasStream {
						jobs = append(jobs, scanJob{cd, resources})
					}
				}
			}
			return
		}
		if pdf.EqualPDFValue(d.Entries.Get("Type"), pdf.PDFName{Value: "Font"}) &&
			pdf.EqualPDFValue(d.Entries.Get("Subtype"), pdf.PDFName{Value: "Type3"}) {
			if procs, ok := d.Entries.Get("CharProcs").(pdf.PDFDict); ok {
				for _, proc := range procs.Entries.All() {
					if pd, ok := proc.(pdf.PDFDict); ok && pd.HasStream {
						jobs = append(jobs, scanJob{pd, resources})
					}
				}
			}
		}
	})

	locals := make([]map[string]int, len(jobs))
	if workers := min(runtime.NumCPU(), len(jobs)); workers > 0 {
		ch := make(chan int)
		var wg sync.WaitGroup
		wg.Add(workers)
		for range workers {
			go func() {
				defer wg.Done()
				for i := range ch {
					local := map[string]int{}
					scanContent(jobs[i].dict, jobs[i].resources, local)
					locals[i] = local
				}
			}()
		}
		for i := range jobs {
			ch <- i
		}
		close(ch)
		wg.Wait()
	}
	for _, l := range locals {
		for k, v := range l {
			counts[k] += v
		}
	}

	return counts
}

// scanJob is one content-stream root (a page's content, or a Type3 CharProc)
// together with the resources in effect for it, queued for parallel scanning.
type scanJob struct {
	dict      pdf.PDFDict
	resources pdf.PDFDict
}

// validPDFAOutputIntentN returns the /N value of the first OutputIntent that meets all PDF/A-1 and ICC profile checks.
// If multiple intents exist, they must use the same profile object, or the entire array is treated as invalid.
func validPDFAOutputIntentN(root pdf.PDFDict) (n int, ok bool) {
	intents, ok := root.Entries.Get("OutputIntents").(pdf.PDFArray)
	if !ok {
		return 0, false
	}

	var firstProfile pdf.PDFValue
	for _, v := range intents {
		intent, ok := v.(pdf.PDFDict)
		if !ok {
			continue
		}
		profile := intent.Entries.Get("DestOutputProfile")
		if profile == nil {
			continue
		}
		if firstProfile == nil {
			firstProfile = profile
		} else if !pdf.EqualPDFValue(firstProfile, profile) {
			return 0, false
		}
	}

	for _, v := range intents {
		intent, ok := v.(pdf.PDFDict)
		if !ok {
			continue
		}
		if !pdf.EqualPDFValue(intent.Entries.Get("S"), pdf.PDFName{Value: "GTS_PDFA1"}) {
			continue
		}
		if intent.Entries.Get("OutputConditionIdentifier") == nil {
			continue
		}
		profile, ok := intent.Entries.Get("DestOutputProfile").(pdf.PDFDict)
		if !ok || !profile.HasStream {
			continue
		}
		nVal, ok := profile.Entries.Get("N").(pdf.PDFInteger)
		if !ok {
			continue
		}
		switch int(nVal) {
		case 1, 3, 4:
		default:
			continue
		}
		if verify.ValidateICCProfileStream(profile) != nil {
			continue
		}
		return int(nVal), true
	}
	return 0, false
}

// iccBasedProfileFixer remediates both halves of 6.2.3.2: an ICCBased colour
// space whose embedded profile disagrees with the /N it declares, and one whose
// profile is a kind PDF/A does not allow. Both are the same repair -- swap in a
// profile that fits -- so one fixer covers them.
//
// The repair goes that way round, never the other: /N is how many operands the
// content streams pass to sc and scn, so changing it would turn every colour
// operator in the document into a malformed one. The profile is the part
// nothing else depends on, so the profile is what gets replaced.
type iccBasedProfileFixer struct{}

func (iccBasedProfileFixer) Applies(c pdf.Check) bool {
	return c == pdf.Checks.Colour.ICCBasedComponentsMismatch ||
		c == pdf.Checks.Colour.ICCBasedProfileInvalid
}

func (iccBasedProfileFixer) Fix(trailer *pdf.PDFDict, _ []pdf.PDFError) (bool, error) {
	changed := false
	for _, arr := range collectICCBasedSpaces(*trailer) {
		if fixICCBasedSpace(arr) {
			changed = true
		}
	}
	return changed, nil
}

// collectICCBasedSpaces finds every ICCBased colour space in the graph. It
// collects before editing so nothing is rewritten mid-walk.
func collectICCBasedSpaces(trailer pdf.PDFDict) []pdf.PDFArray {
	var out []pdf.PDFArray
	visited := map[uintptr]bool{}

	var walk func(v pdf.PDFValue)
	consider := func(child pdf.PDFValue) {
		if arr, ok := child.(pdf.PDFArray); ok && isICCBasedSpace(arr) {
			out = append(out, arr)
		}
	}
	walk = func(v pdf.PDFValue) {
		switch val := v.(type) {
		case pdf.PDFDict:
			ptr := pdf.ValuePointer(val.Entries)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			for k, child := range val.Entries.All() {
				if k == "_ref" || k == "_dirty" {
					continue
				}
				consider(child)
				walk(child)
			}
		case pdf.PDFArray:
			ptr := pdf.ArrayPointer(val)
			if visited[ptr] {
				return
			}
			visited[ptr] = true
			for _, child := range val {
				consider(child)
				walk(child)
			}
		}
	}
	walk(trailer)
	return out
}

// isICCBasedSpace reports whether arr is an [/ICCBased stream] colour space.
func isICCBasedSpace(arr pdf.PDFArray) bool {
	if len(arr) < 2 {
		return false
	}
	head, ok := arr[0].(pdf.PDFName)
	if !ok || head.Value != "ICCBased" {
		return false
	}
	stream, ok := arr[1].(pdf.PDFDict)
	return ok && stream.HasStream
}

// fixICCBasedSpace repairs one ICCBased colour space, re-checking the
// predicate so an already-fixed space is a no-op.
func fixICCBasedSpace(arr pdf.PDFArray) bool {
	stream := arr[1].(pdf.PDFDict)
	data, err := pdf.DecodeStream(stream)
	usable := err == nil && len(data) >= 128 && verify.ICCInputProfileDefect(data) == ""
	if usable && verify.ICCComponentsMismatch(stream, data) == "" {
		return false
	}

	// When the profile itself is fine, /N can simply be made to say what that
	// profile holds -- nothing in the file depends on a missing or out-of-range
	// /N. A usable profile always has a component count, since that is part of
	// what makes it usable.
	n, ok := stream.Entries.Get("N").(pdf.PDFInteger)
	if !ok || (n != 1 && n != 3 && n != 4) {
		if usable {
			want, _ := verify.ICCColourSpaceComponents(string(data[16:20]))
			stream.Entries.Set("N", pdf.PDFInteger(want))
			return true
		}
		n = 3
	}

	// Otherwise the profile goes, and a bundled one of the right size takes its
	// place. /N stays as it was, so every sc and scn in the file keeps passing
	// the number of operands it always did.
	profile := srgbICCProfile
	switch n {
	case 1:
		profile = grayICCProfile
	case 4:
		profile = cmykICCProfile
	}
	stream.Entries.Set("N", pdf.PDFInteger(n))
	stream.Entries.Del("Filter")
	stream.Entries.Del("DecodeParms")
	stream.HasStream = true
	stream.RawStream = profile
	arr[1] = stream
	return true
}
