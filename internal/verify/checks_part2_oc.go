package verify

import (
	"errors"
	"fmt"
	"strings"

	"github.com/voidrab/gopdfrab/internal/pdf"
)

// 6.9 Optional content

// maxOrderDepth bounds the nesting of an Order array, so that a pathologically
// deep one cannot exhaust the stack.
const maxOrderDepth = 64

// verifyOCConfigs checks the optional content configuration dictionaries of
// root's OCProperties against ISO 19005-2 6.9, as veraPDF reads it.
func verifyOCConfigs(root pdf.PDFDict) []pdf.PDFError {
	props, ok := root.Entries.Get("OCProperties").(pdf.PDFDict)
	if !ok {
		return nil
	}
	var errs []pdf.PDFError
	report := func(c pdf.Check, cfg pdf.PDFDict, msg string) {
		subject, ref := "a direct optional content configuration", refOf(root)
		if r, ok := cfg.Ref(); ok {
			subject, ref = fmt.Sprintf("optional content configuration %d %d R", r.ObjNum, r.GenNum), &r
		}
		errs = append(errs, pdf.NewError(c, []error{errors.New(subject + " " + msg)}, 0, ref))
	}
	configs := OCConfigs(props)
	dups := OCConfigDuplicates(configs)
	groupNames := OCGroupNames(OCGroups(props))
	for i, cfg := range configs {
		name, named := OCConfigName(cfg)
		if !named {
			report(pdf.Checks.Structure.OCConfigMissingName, cfg, "has no non-empty Name string")
		}
		if dups[i] {
			report(pdf.Checks.Structure.OCConfigDuplicateName, cfg, fmt.Sprintf("repeats the Name %q", name))
		}
		if missing := OCGroupsMissingFromOrder(cfg, groupNames); len(missing) > 0 {
			quoted := make([]string, len(missing))
			for j, g := range missing {
				quoted[j] = fmt.Sprintf("%q", groupNames[g])
			}
			report(pdf.Checks.Structure.OCConfigOrderIncomplete, cfg,
				"has an Order that does not list the group(s) "+strings.Join(quoted, ", "))
		}
		if _, ok := cfg.Entries.Lookup("AS"); ok {
			report(pdf.Checks.Structure.OCConfigAutoState, cfg, "contains an AS entry")
		}
	}
	return errs
}

// OCConfigs returns the optional content configuration dictionaries of props,
// an OCProperties dictionary: the default one, D, first, then each dictionary
// of the Configs array. Values that are not dictionaries are skipped.
func OCConfigs(props pdf.PDFDict) []pdf.PDFDict {
	var configs []pdf.PDFDict
	if d, ok := props.Entries.Get("D").(pdf.PDFDict); ok {
		configs = append(configs, d)
	}
	return append(configs, dictsOf(props.Entries.Get("Configs"))...)
}

// dictsOf returns the dictionaries among the elements of v, when it is an
// array.
func dictsOf(v pdf.PDFValue) []pdf.PDFDict {
	arr, _ := v.(pdf.PDFArray)
	var dicts []pdf.PDFDict
	for _, e := range arr {
		if d, ok := e.(pdf.PDFDict); ok {
			dicts = append(dicts, d)
		}
	}
	return dicts
}

// textEntry returns the decoded text of d's key entry and reports whether
// that entry is a string, literal or hexadecimal. A name object is not a
// string.
func textEntry(d pdf.PDFDict, key string) (string, bool) {
	raw, ok := pdf.StringBytes(d.Entries.Get(key))
	if !ok {
		return "", false
	}
	return pdf.DecodePDFTextString(raw), true
}

// OCConfigName returns the Name of the configuration dictionary cfg and
// reports whether that Name is a non-empty string.
func OCConfigName(cfg pdf.PDFDict) (string, bool) {
	name, ok := textEntry(cfg, "Name")
	return name, ok && name != ""
}

// OCConfigDuplicates reports, for each of configs in order, whether a
// configuration before it has a Name string equal to its own. The first
// configuration with a given name keeps it, so the default one, D, never
// repeats a name.
func OCConfigDuplicates(configs []pdf.PDFDict) []bool {
	dup := make([]bool, len(configs))
	seen := map[string]bool{}
	for i, cfg := range configs {
		name, ok := textEntry(cfg, "Name")
		if !ok {
			continue
		}
		dup[i] = seen[name]
		seen[name] = true
	}
	return dup
}

// OCGroups returns the optional content group dictionaries that the OCGs
// array of props, an OCProperties dictionary, lists.
func OCGroups(props pdf.PDFDict) []pdf.PDFDict {
	return dictsOf(props.Entries.Get("OCGs"))
}

// OCGroupNames returns the Name string of each of groups, or the empty
// string for a group that has none.
func OCGroupNames(groups []pdf.PDFDict) []string {
	names := make([]string, len(groups))
	for i, g := range groups {
		names[i], _ = textEntry(g, "Name")
	}
	return names
}

// OCGroupsMissingFromOrder returns the indices into names, the group names
// OCGroupNames returns, of the groups that the Order array of the
// configuration dictionary cfg does not list. As in veraPDF, a group is
// listed when a dictionary anywhere in Order, including in nested arrays,
// has a Name string equal to the group's name; a group without a Name
// string counts as named with the empty string. A configuration whose Order
// is absent or not an array is treated as listing every group.
func OCGroupsMissingFromOrder(cfg pdf.PDFDict, names []string) []int {
	order, ok := cfg.Entries.Get("Order").(pdf.PDFArray)
	if !ok || len(names) == 0 {
		return nil
	}
	listed := map[string]bool{}
	collectOrderNames(order, listed, map[uintptr]bool{}, 0)
	var missing []int
	for i, name := range names {
		if !listed[name] {
			missing = append(missing, i)
		}
	}
	return missing
}

// collectOrderNames adds to listed the Name string of each dictionary in
// arr and in the arrays nested in it. seen holds the arrays already visited,
// by pdf.ArrayPointer, so an array reached twice, or from within itself, is
// read once.
func collectOrderNames(arr pdf.PDFArray, listed map[string]bool, seen map[uintptr]bool, depth int) {
	if depth > maxOrderDepth || seen[pdf.ArrayPointer(arr)] {
		return
	}
	seen[pdf.ArrayPointer(arr)] = true
	for _, v := range arr {
		switch v := v.(type) {
		case pdf.PDFArray:
			collectOrderNames(v, listed, seen, depth+1)
		case pdf.PDFDict:
			if name, ok := textEntry(v, "Name"); ok {
				listed[name] = true
			}
		}
	}
}
