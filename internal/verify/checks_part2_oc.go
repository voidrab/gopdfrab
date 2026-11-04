package verify

import (
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
	report := func(c pdf.Check, cfg pdf.PDFDict, format string, args ...any) {
		ref := refOf(cfg)
		if ref == nil {
			ref = refOf(root)
		}
		subject := "a direct optional content configuration"
		if r, ok := cfg.Ref(); ok {
			subject = fmt.Sprintf("optional content configuration %d %d R", r.ObjNum, r.GenNum)
		}
		errs = append(errs, pdf.NewError(c, []error{fmt.Errorf("%s "+format, append([]any{subject}, args...)...)}, 0, ref))
	}
	configs := OCConfigs(props)
	groups := OCGroups(props)
	for i, dup := range OCConfigDuplicates(configs) {
		cfg := configs[i]
		name, named := OCConfigName(cfg)
		if !named {
			report(pdf.Checks.Structure.OCConfigMissingName, cfg, "has no non-empty Name string")
		}
		if dup {
			report(pdf.Checks.Structure.OCConfigDuplicateName, cfg, "repeats the Name %q", name)
		}
		if missing := OCGroupsMissingFromOrder(cfg, groups); len(missing) > 0 {
			names := make([]string, len(missing))
			for j, g := range missing {
				names[j] = fmt.Sprintf("%q", OCGroupName(g))
			}
			report(pdf.Checks.Structure.OCConfigOrderIncomplete, cfg,
				"has an Order that does not list the group(s) %s", strings.Join(names, ", "))
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
	arr, _ := props.Entries.Get("Configs").(pdf.PDFArray)
	for _, v := range arr {
		if cfg, ok := v.(pdf.PDFDict); ok {
			configs = append(configs, cfg)
		}
	}
	return configs
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
	arr, _ := props.Entries.Get("OCGs").(pdf.PDFArray)
	var groups []pdf.PDFDict
	for _, v := range arr {
		if g, ok := v.(pdf.PDFDict); ok {
			groups = append(groups, g)
		}
	}
	return groups
}

// OCGroupName returns the Name string of the group dictionary g, or the empty
// string when it has none.
func OCGroupName(g pdf.PDFDict) string {
	name, _ := textEntry(g, "Name")
	return name
}

// OCGroupsMissingFromOrder returns the groups that the Order array of the
// configuration dictionary cfg does not list. As in veraPDF, a group is
// listed when a dictionary anywhere in Order, including in nested arrays,
// has a Name string equal to the group's name; a group without a Name
// string counts as named with the empty string. A configuration whose Order
// is absent or not an array is treated as listing every group.
func OCGroupsMissingFromOrder(cfg pdf.PDFDict, groups []pdf.PDFDict) []pdf.PDFDict {
	order, ok := cfg.Entries.Get("Order").(pdf.PDFArray)
	if !ok || len(groups) == 0 {
		return nil
	}
	listed := map[string]bool{}
	collectOrderNames(order, listed, map[*pdf.PDFValue]bool{}, 0)
	var missing []pdf.PDFDict
	for _, g := range groups {
		if !listed[OCGroupName(g)] {
			missing = append(missing, g)
		}
	}
	return missing
}

// collectOrderNames adds to listed the Name string of each dictionary in
// arr and in the arrays nested in it. seen holds the arrays already visited,
// so an array reached twice, or from within itself, is read once.
func collectOrderNames(arr pdf.PDFArray, listed map[string]bool, seen map[*pdf.PDFValue]bool, depth int) {
	if len(arr) == 0 || depth > maxOrderDepth || seen[&arr[0]] {
		return
	}
	seen[&arr[0]] = true
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
