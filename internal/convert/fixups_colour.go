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

func init() {
	registerPreemptiveFixup(injectOutputIntent)
}

// colourModelN maps dominantColourModel's "rgb"/"cmyk" result to the /N an
// OutputIntent's ICC profile must declare to cover it. "gray" deliberately
// has no entry: it never needs a specific /N (see dominantColourModel).
var colourModelN = map[string]int{"rgb": 3, "cmyk": 4}

// injectOutputIntent ensures the document's catalog has a PDF/A OutputIntent
// backed by an embedded ICC profile.
func injectOutputIntent(trailer *pdf.PDFDict) error {
	root, ok := trailer.Entries["Root"].(pdf.PDFDict)
	if !ok {
		return fmt.Errorf("injectOutputIntent: Root is not a dictionary")
	}

	dominant := dominantColourModel(detectColourModelUsage(*trailer))
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
	profile.Entries["N"] = pdf.PDFInteger(wantN)
	profile.Entries["Alternate"] = pdf.PDFName{Value: alternate}
	profile.HasStream = true
	profile.RawStream = iccBytes

	intent := pdf.NewPDFDict()
	intent.Entries["Type"] = pdf.PDFName{Value: "OutputIntent"}
	intent.Entries["S"] = pdf.PDFName{Value: "GTS_PDFA1"}
	intent.Entries["OutputConditionIdentifier"] = pdf.PDFString{Value: identifier}
	intent.Entries["Info"] = pdf.PDFString{Value: identifier + " ICC profile injected by gopdfrab"}
	intent.Entries["DestOutputProfile"] = profile

	root.Entries["OutputIntents"] = pdf.PDFArray{intent}
	trailer.Entries["Root"] = root
	return nil
}

// iccBasedColourSpace builds a "[/ICCBased <stream>]" colour-space array
// backed by profile (an embedded ICC v2 profile of n components), suitable
// for a /DefaultRGB or /DefaultCMYK resource entry.
func iccBasedColourSpace(n int, profile []byte) pdf.PDFArray {
	stream := pdf.NewPDFDict()
	stream.Entries["N"] = pdf.PDFInteger(n)
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

func resourcesOf(dict, fallback pdf.PDFDict) pdf.PDFDict {
	if res, ok := dict.Entries["Resources"].(pdf.PDFDict); ok {
		return res
	}
	return fallback
}

// detectColourModelUsage counts how often RGB, Gray, and CMYK color models appear in the document graph.
// It checks dictionary-level color spaces everywhere, but only counts content-stream operators and inline
// images where they are actually used.
func detectColourModelUsage(trailer pdf.PDFDict) map[string]int {
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
		countModel := func(name string, resources pdf.PDFDict) {
			if m, ok := verify.InlineCSAbbrev[name]; ok {
				countModelExempt(m, resources)
				return
			}
			countModelExempt(verify.NamedColourModel(pdf.PDFName{Value: name}, resources), resources)
		}
		var scan func(dict, resources pdf.PDFDict)
		scan = func(dict, resources pdf.PDFDict) {
			if !claimScan(pdf.ValuePointer(dict.Entries)) {
				return
			}
			data, err := pdf.DecodeStream(dict)
			if err != nil {
				return
			}
			pdf.NewContentScanner(data).Scan(func(op string, operands []pdf.PDFValue) {
				switch op {
				case "rg", "RG":
					countModelExempt("rgb", resources)
				case "g", "G":
					countModelExempt("gray", resources)
				case "k", "K":
					countModelExempt("cmyk", resources)
				case "cs", "CS":
					if len(operands) != 1 {
						return
					}
					if name, ok := operands[0].(pdf.PDFName); ok {
						countModel(name.Value, resources)
					}
				case "INLINEIMAGE":
					for i := 0; i+1 < len(operands); i += 2 {
						key, ok := operands[i].(pdf.PDFName)
						if !ok || (key.Value != "CS" && key.Value != "ColorSpace") {
							continue
						}
						if name, ok := operands[i+1].(pdf.PDFName); ok {
							countModel(name.Value, resources)
						}
					}
				case "Do":
					if len(operands) != 1 {
						return
					}
					name, ok := operands[0].(pdf.PDFName)
					if !ok {
						return
					}
					xobjects, _ := resources.Entries["XObject"].(pdf.PDFDict)
					if form, ok := xobjects.Entries[name.Value].(pdf.PDFDict); ok && form.HasStream {
						scan(form, resourcesOf(form, resources))
					}
				case "scn", "SCN":
					if len(operands) == 0 {
						return
					}
					name, ok := operands[len(operands)-1].(pdf.PDFName)
					if !ok {
						return
					}
					patterns, _ := resources.Entries["Pattern"].(pdf.PDFDict)
					if pat, ok := patterns.Entries[name.Value].(pdf.PDFDict); ok && pat.HasStream {
						scan(pat, resourcesOf(pat, resources))
					}
				}
			})
		}
		scan(root, rootRes)
	}

	var jobs []scanJob
	walkDicts(trailer, map[uintptr]bool{}, func(d pdf.PDFDict) {
		if model := verify.DeviceColourModel(d.Entries["ColorSpace"]); model != "" {
			counts[model]++
		}

		for _, v := range d.Entries {
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

		resources, _ := d.Entries["Resources"].(pdf.PDFDict)

		if pdf.EqualPDFValue(d.Entries["Type"], pdf.PDFName{Value: "Page"}) {
			switch contents := d.Entries["Contents"].(type) {
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
		if pdf.EqualPDFValue(d.Entries["Type"], pdf.PDFName{Value: "Font"}) &&
			pdf.EqualPDFValue(d.Entries["Subtype"], pdf.PDFName{Value: "Type3"}) {
			if procs, ok := d.Entries["CharProcs"].(pdf.PDFDict); ok {
				for _, proc := range procs.Entries {
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
	intents, ok := root.Entries["OutputIntents"].(pdf.PDFArray)
	if !ok {
		return 0, false
	}

	var firstProfile pdf.PDFValue
	for _, v := range intents {
		intent, ok := v.(pdf.PDFDict)
		if !ok {
			continue
		}
		profile := intent.Entries["DestOutputProfile"]
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
		if !pdf.EqualPDFValue(intent.Entries["S"], pdf.PDFName{Value: "GTS_PDFA1"}) {
			continue
		}
		if intent.Entries["OutputConditionIdentifier"] == nil {
			continue
		}
		profile, ok := intent.Entries["DestOutputProfile"].(pdf.PDFDict)
		if !ok || !profile.HasStream {
			continue
		}
		nVal, ok := profile.Entries["N"].(pdf.PDFInteger)
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
