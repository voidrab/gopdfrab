//go:build ignore

// Command gen reads the vendored Arlington PDF Model TSVs for one PDF version
// under testdata/tsv/<version>, builds one ObjectType per TSV file, and writes
// them with arlington.EncodeModel to model-<version>.bin, which the package
// embeds and decodes on first use.
//
// Invoke via `go generate ./internal/arlington/...`. The -version flag picks
// the TSV set (1.4 or 1.7), and -out writes elsewhere; the idempotence test
// regenerates into a scratch file and compares.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/voidrab/gopdfrab/internal/arlington"
)

const latestDir = "testdata/tsv/latest"

var (
	// modelVersion is the PDF version that version-gate predicates are
	// folded against. It names the vendored TSV subset under tsvDir and is
	// set from the -version flag.
	modelVersion string
	tsvDir       string
)

// valueTypeIdent maps an Arlington TSV Type token to the matching Go identifier declared in
// arlington.go. Keep in sync with the ValueType const block there.
var valueTypeIdent = map[string]string{
	"array":        "Array",
	"bitmask":      "Bitmask",
	"boolean":      "Boolean",
	"date":         "Date",
	"dictionary":   "Dictionary",
	"integer":      "Integer",
	"matrix":       "Matrix",
	"name":         "Name",
	"name-tree":    "NameTree",
	"null":         "Null",
	"number":       "Number",
	"number-tree":  "NumberTree",
	"rectangle":    "Rectangle",
	"stream":       "Stream",
	"string":       "String",
	"string-ascii": "StringASCII",
	"string-byte":  "StringByte",
	"string-text":  "StringText",
}

// row is one parsed TSV data line, columns in file order.
type row struct {
	key, typ, sinceVersion, deprecatedIn, required, indirectRef,
	inheritable, defaultValue, possibleValues, specialCase, link, note string
}

// linkGroup is the generator's form of an arlington.LinkGroup.
type linkGroup struct {
	valueTypes    []string // Go ValueType identifiers this alternative applies to; nil means "the only alternative"
	candidates    []string
	discriminator string
	byValue       map[string]string // discriminator value -> candidate type name
}

// predication mirrors arlington.Predication.
type predication struct {
	required, types, values, indirect bool
}

func (p predication) any() bool { return p.required || p.types || p.values || p.indirect }

// keyDef is the generator's form of an arlington.KeyDef.
type keyDef struct {
	name              string
	types             []string // Go identifiers, e.g. "Dictionary"
	required          bool
	requiredWhen      *condExpr
	valueCond         *condExpr
	indirectReference string // Go identifier, e.g. "IndirectEither"
	sinceVersion      string
	deprecatedIn      string
	possibleValues    []string
	pinnedValues      []pinnedExpr
	specialCase       *condExpr
	linkGroups        []linkGroup
	predicated        predication
	inheritable       bool
}

// typeEntry is one parsed Arlington type (one TSV file), before discriminator resolution.
type typeEntry struct {
	name     string
	keys     []keyDef
	wildcard *keyDef
}

func main() {
	flag.StringVar(&modelVersion, "version", "1.4", "the PDF version whose TSV set to read")
	out := flag.String("out", "", "where to write the encoded model (default model-<version>.bin)")
	flag.Parse()
	tsvDir = filepath.Join("testdata", "tsv", modelVersion)
	if *out == "" {
		*out = "model-" + modelVersion + ".bin"
	}

	files, err := filepath.Glob(filepath.Join(tsvDir, "*.tsv"))
	if err != nil || len(files) == 0 {
		log.Fatalf("glob %s: %v (found %d files)", tsvDir, err, len(files))
	}
	sort.Strings(files)

	laterKeys, err := computeLaterKeys()
	if err != nil {
		log.Fatalf("computing keys introduced after PDF %s: %v", modelVersion, err)
	}

	// First pass: parse every type's own keys. keyDefsByType is needed by the second pass to
	// look up each LinkGroup candidate's own schema (its discriminator key, if any).
	var types []typeEntry
	keyDefsByType := map[string][]keyDef{}

	totalRows, simpleRows, specialCases := 0, 0, 0
	for _, path := range files {
		typeName := strings.TrimSuffix(filepath.Base(path), ".tsv")
		rows, err := parseTSV(path)
		if err != nil {
			log.Fatalf("%s: %v", path, err)
		}

		var keys []keyDef
		var wildcard *keyDef
		for _, r := range rows {
			totalRows++
			kd := buildKeyDef(r)
			if req, ok := requiredOverrides[[2]string{typeName, kd.name}]; ok {
				kd.required = req
				kd.requiredWhen = nil
				kd.predicated.required = false
			}
			if _, ok := specialCaseOverrides[[2]string{typeName, kd.name}]; ok {
				kd.specialCase = nil
			}
			if !kd.predicated.any() {
				simpleRows++
			}
			if kd.specialCase != nil {
				specialCases++
			}
			if kd.name == "*" {
				w := kd
				wildcard = &w
				continue
			}
			keys = append(keys, kd)
		}
		types = append(types, typeEntry{name: typeName, keys: keys, wildcard: wildcard})
		keyDefsByType[typeName] = keys
	}

	// Second pass: now that every type's own keys are known, resolve a discriminator for every
	// LinkGroup with more than one candidate.
	for i := range types {
		for j := range types[i].keys {
			resolveDiscriminators(&types[i].keys[j], keyDefsByType)
		}
		if types[i].wildcard != nil {
			resolveDiscriminators(types[i].wildcard, keyDefsByType)
		}
	}

	model := make(map[string]arlington.ObjectType, len(types))
	for _, t := range types {
		ot := arlington.ObjectType{Name: t.name, LaterKeys: laterKeys[t.name]}
		for _, kd := range t.keys {
			ot.Keys = append(ot.Keys, toKeyDef(kd))
		}
		if t.wildcard != nil {
			w := toKeyDef(*t.wildcard)
			ot.Wildcard = &w
		}
		model[t.name] = ot
	}

	fraction := 0.0
	if totalRows > 0 {
		fraction = float64(simpleRows) / float64(totalRows) * 100
	}
	fmt.Fprintf(os.Stderr, "arlington: %d/%d rows simple (%.1f%%), %d types, %d SpecialCase constraints\n",
		simpleRows, totalRows, fraction, len(files), specialCases)

	if err := os.WriteFile(*out, arlington.EncodeModel(model, selfIdentified(types)), 0o644); err != nil {
		log.Fatalf("write %s: %v", *out, err)
	}
}

// valueTypeByIdent maps a ValueType's Go identifier (valueTypeIdent's values) to the constant.
var valueTypeByIdent = func() map[string]arlington.ValueType {
	m := map[string]arlington.ValueType{}
	for vt := arlington.Array; vt <= arlington.StringText; vt++ {
		m[valueTypeIdent[vt.String()]] = vt
	}
	return m
}()

var indirectByIdent = map[string]arlington.IndirectRule{
	"IndirectEither":    arlington.IndirectEither,
	"IndirectRequired":  arlington.IndirectRequired,
	"IndirectForbidden": arlington.IndirectForbidden,
}

var condOpByIdent = map[string]arlington.CondOp{
	"CondPresent":   arlington.CondPresent,
	"CondEq":        arlington.CondEq,
	"CondNe":        arlington.CondNe,
	"CondLt":        arlington.CondLt,
	"CondLe":        arlington.CondLe,
	"CondGt":        arlington.CondGt,
	"CondGe":        arlington.CondGe,
	"CondAnd":       arlington.CondAnd,
	"CondOr":        arlington.CondOr,
	"CondNot":       arlington.CondNot,
	"CondUnknown":   arlington.CondUnknown,
	"CondContains":  arlington.CondContains,
	"CondNotStd14":  arlington.CondNotStd14,
	"CondBitsClear": arlington.CondBitsClear,
	"CondBitsSet":   arlington.CondBitsSet,
}

var condFnByIdent = map[string]arlington.CondFn{
	"":               arlington.FnValue,
	"FnValue":        arlington.FnValue,
	"FnArrayLength":  arlington.FnArrayLength,
	"FnStringLength": arlington.FnStringLength,
}

// lookup returns m[ident], failing generation on an identifier the maps above do not know.
func lookup[T any](m map[string]T, ident string) T {
	v, ok := m[ident]
	if !ok {
		log.Fatalf("no constant for identifier %q", ident)
	}
	return v
}

func toValueTypes(idents []string) []arlington.ValueType {
	var out []arlington.ValueType
	for _, id := range idents {
		out = append(out, lookup(valueTypeByIdent, id))
	}
	return out
}

func toCondPtr(c *condExpr) *arlington.Cond {
	if c == nil {
		return nil
	}
	ac := toCond(*c)
	return &ac
}

// toCond converts a condExpr. The second-entry right operand fields only exist alongside
// rhsKey, and BitHi alongside BitLo, so stray values elsewhere are dropped.
func toCond(c condExpr) arlington.Cond {
	ac := arlington.Cond{
		Op:    lookup(condOpByIdent, c.op),
		Key:   c.key,
		Value: c.value,
		Fn:    lookup(condFnByIdent, c.fn),
		Mod:   c.mod,
	}
	if c.rhsKey != "" {
		ac.RHSKey = c.rhsKey
		ac.RHSFn = lookup(condFnByIdent, c.rhsFn)
		ac.RHSAdd = c.rhsAdd
		ac.RHSMul = c.rhsMul
		ac.RHSKey2 = c.rhsKey2
	}
	if c.bitLo != 0 {
		ac.BitLo, ac.BitHi = c.bitLo, c.bitHi
	}
	for _, k := range c.kids {
		ac.Kids = append(ac.Kids, toCond(k))
	}
	return ac
}

func nilIfEmpty(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	return ss
}

func toKeyDef(kd keyDef) arlington.KeyDef {
	out := arlington.KeyDef{
		Name:              kd.name,
		Types:             toValueTypes(kd.types),
		Required:          kd.required,
		RequiredWhen:      toCondPtr(kd.requiredWhen),
		ValueCond:         toCondPtr(kd.valueCond),
		IndirectReference: lookup(indirectByIdent, kd.indirectReference),
		SinceVersion:      kd.sinceVersion,
		DeprecatedIn:      kd.deprecatedIn,
		PossibleValues:    nilIfEmpty(kd.possibleValues),
		SpecialCase:       toCondPtr(kd.specialCase),
		Inheritable:       kd.inheritable,
		Predicated: arlington.Predication{
			Required: kd.predicated.required,
			Types:    kd.predicated.types,
			Values:   kd.predicated.values,
			Indirect: kd.predicated.indirect,
		},
	}
	for _, p := range kd.pinnedValues {
		c := toCond(p.cond)
		out.PinnedValues = append(out.PinnedValues, arlington.PinnedValue{When: &c, Value: p.value})
	}
	for _, g := range kd.linkGroups {
		lg := arlington.LinkGroup{
			ValueTypes: toValueTypes(g.valueTypes),
			Candidates: nilIfEmpty(g.candidates),
		}
		if g.discriminator != "" {
			lg.Discriminator = g.discriminator
			if len(g.byValue) > 0 {
				lg.ByValue = g.byValue
			}
		}
		out.LinkGroups = append(out.LinkGroups, lg)
	}
	return out
}

// selfIdentified builds the unambiguous self-identification table: (Type value, Subtype
// value) pairs that exactly one Arlington type is consistent with. Each type claims every
// (Type, Subtype) combination its enumerated PossibleValues allow, so overlapping claims
// collide and are dropped; a bare (Type, "") claim survives only when no other type
// constrains that Type value at all. Fail closed: any collision drops the pair. Predicated
// or unconstrained Type/Subtype rows do not occur in the vendored 1.4 set.
func selfIdentified(types []typeEntry) map[[2]string]string {
	enumPV := func(keys []keyDef, name string) []string {
		for _, kd := range keys {
			if kd.name == name && !kd.predicated.values {
				return kd.possibleValues
			}
		}
		return nil
	}
	claims := map[[2]string][]string{}
	for _, t := range types {
		tvs := enumPV(t.keys, "Type")
		svs := enumPV(t.keys, "Subtype")
		if len(svs) == 0 {
			svs = []string{""}
		}
		for _, tv := range tvs {
			for _, sv := range svs {
				claims[[2]string{tv, sv}] = append(claims[[2]string{tv, sv}], t.name)
			}
		}
	}
	perType := map[string]int{}
	for k := range claims {
		perType[k[0]]++
	}
	var keys [][2]string
	for k, names := range claims {
		if len(names) != 1 {
			continue
		}
		if k[1] == "" && perType[k[0]] > 1 {
			continue // another type also constrains this Type value: a bare match is ambiguous
		}
		keys = append(keys, k)
	}
	out := make(map[[2]string]string, len(keys))
	for _, k := range keys {
		out[k] = claims[k][0]
	}
	fmt.Fprintf(os.Stderr, "arlington: %d self-identifying (Type, Subtype) pairs\n", len(keys))
	return out
}

// compileValueCond compiles a PossibleValues column that is exactly one whole-group
// fn:Eval(...) constraint into a runtime condition on the key's value. Multi-group columns
// (per-type-alternative constraints) are not compiled: the matching alternative is unknown
// at runtime, so enforcing all groups could flag a legal value. ok with a nil tree means the
// constraint resolved to nothing enforceable (vacuous or constant-true).
func compileValueCond(raw string) (tree *condExpr, ok bool) {
	groups := splitGroups(raw)
	if len(groups) != 1 {
		return nil, false
	}
	items := splitTopLevelComma(groups[0])
	if len(items) != 1 || !strings.HasPrefix(strings.TrimSpace(items[0]), "fn:Eval(") {
		return nil, false
	}
	res := compileCond(strings.TrimSpace(items[0]))
	switch {
	case !res.ok:
		return nil, false
	case res.vacuous || (res.isConst && res.constVal):
		return nil, true
	case res.isConst:
		return nil, false // a constant-false constraint would flag every value: fail closed
	default:
		return res.tree, true
	}
}

// computeLaterKeys returns, per type name (TSV basename), the keys present in
// testdata/tsv/latest but absent from tsvDir. These are the keys the Arlington
// PDF Model says were introduced after modelVersion. A type absent from
// tsv/latest (renamed across versions) is omitted, which yields an empty, safe
// (false-negative) LaterKeys for it.
func computeLaterKeys() (map[string][]string, error) {
	keysOld, err := keySetsByType(tsvDir)
	if err != nil {
		return nil, err
	}
	keysLatest, err := keySetsByType(latestDir)
	if err != nil {
		return nil, err
	}

	later := make(map[string][]string, len(keysLatest))
	for typeName, latestKeys := range keysLatest {
		old := keysOld[typeName]
		var added []string
		for k := range latestKeys {
			if k == "*" || old[k] {
				continue
			}
			added = append(added, k)
		}
		if len(added) > 0 {
			sort.Strings(added)
			later[typeName] = added
		}
	}
	return later, nil
}

// keySetsByType reads every TSV under dir and returns, per type name, the set of Key column
// values (order not preserved; only membership matters). Unlike parseTSV, this only reads the
// first column and tolerates rows with fewer than 12 columns -- tsv/latest, being upstream's
// unfiltered live set, has a handful of vendor-extension rows short a trailing empty column.
func keySetsByType(dir string) (map[string]map[string]bool, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.tsv"))
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]bool, len(files))
	for _, path := range files {
		typeName := strings.TrimSuffix(filepath.Base(path), ".tsv")
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		set := map[string]bool{}
		first := true
		for sc.Scan() {
			if first {
				first = false // header row
				continue
			}
			line := sc.Text()
			if line == "" {
				continue
			}
			if i := strings.IndexByte(line, '\t'); i >= 0 {
				set[line[:i]] = true
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out[typeName] = set
	}
	return out, nil
}

func parseTSV(path string) ([]row, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var rows []row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	first := true
	for sc.Scan() {
		if first {
			first = false // header row
			continue
		}
		line := sc.Text()
		if line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 12 {
			return nil, fmt.Errorf("expected 12 columns, got %d: %q", len(cols), line)
		}
		rows = append(rows, row{
			key: cols[0], typ: cols[1], sinceVersion: cols[2], deprecatedIn: cols[3],
			required: cols[4], indirectRef: cols[5], inheritable: cols[6],
			defaultValue: cols[7], possibleValues: cols[8], specialCase: cols[9],
			link: cols[10], note: cols[11],
		})
	}
	return rows, sc.Err()
}

// requiredOverrides relaxes vendored (type, key) rows whose Required constraint no PDF/A
// validator enforces, so keeping them would flag files the pass corpora accept.
var requiredOverrides = map[[2]string]bool{
	// PDF 1.4 Table 4.41 says "optional but strongly recommended"; Arlington marks it TRUE.
	{"XObjectFormType1", "Resources"}: false,
	// Required-if-PieceInfo-present in ISO 32000; unenforced by validators on real files.
	{"PageObject", "LastModified"}: false,
	// Required in ISO 32000, but real files omit it and no validator flags that.
	{"StructElem", "P"}: false,
}

// specialCaseOverrides drops (type, key) SpecialCase constraints for the same reason
// requiredOverrides exists: no PDF/A validator enforces them and real files violate them
// routinely, so compiling them would flag files the pass corpora accept.
var specialCaseOverrides = map[[2]string]bool{
	// fn:Eval(fn:IsPresent(LastModified)): the SpecialCase mirror of the
	// required-if-PieceInfo rule requiredOverrides already neutralizes on PageObject --
	// LastModified is universally omitted in real files carrying PieceInfo.
	{"PageObject", "PieceInfo"}:             false,
	{"XObjectFormType1", "PieceInfo"}:       false,
	{"XObjectFormPrinterMark", "PieceInfo"}: false,
	{"XObjectFormTrapNet", "PieceInfo"}:     false,
}

// buildKeyDef converts one TSV row into its Go source representation, marking each column
// whose fn: predicate could not be folded so consumers skip exactly that column's check.
func buildKeyDef(r row) keyDef {
	kd := keyDef{name: r.key, sinceVersion: r.sinceVersion, deprecatedIn: r.deprecatedIn}

	for _, tok := range strings.Split(r.typ, ";") {
		ident, ok := valueTypeIdent[tok]
		if !ok {
			kd.predicated.types = true
			continue
		}
		kd.types = append(kd.types, ident)
	}

	switch {
	case r.required == "TRUE":
		kd.required = true
	case strings.Contains(r.required, "fn:"):
		// RequiredWhen is only evaluated against dicts at runtime, so index-operand trees
		// (and index rows generally) stay predicated here.
		switch res := compileRequired(r.required); {
		case res.ok && res.isConst:
			kd.required = res.constVal
		case res.ok && plainKey(kd.name) && condOperandsResolvable(res.tree, kd.name):
			kd.requiredWhen = res.tree
		default:
			kd.predicated.required = true
		}
	}

	kd.indirectReference = parseIndirectReference(r.indirectRef)
	if strings.Contains(r.indirectRef, "fn:") {
		if rule, ok := foldIndirect(r.indirectRef); ok {
			kd.indirectReference = rule
		} else {
			kd.predicated.indirect = true
		}
	}

	kd.inheritable = r.inheritable == "TRUE"

	// ValueCond compiles for named dict keys (operands are sibling key names) and for fixed
	// array indices (operands are element indices, resolved against the owning array at
	// runtime). Wildcard and offset-wildcard rows have no single position to resolve against.
	values, pins, unresolved := parsePossibleValues(r.possibleValues)
	kd.possibleValues = values
	for _, p := range pins {
		if cond := p.cond; condOperandsResolvable(&cond, kd.name) {
			kd.pinnedValues = append(kd.pinnedValues, p)
		}
	}
	if unresolved {
		if tree, ok := compileValueCond(r.possibleValues); ok && condOperandsResolvable(tree, kd.name) {
			kd.valueCond = tree
			kd.possibleValues = nil
		} else {
			kd.predicated.values = true
		}
	}
	kd.specialCase = compileSpecialCase(r.specialCase, kd.name)
	kd.linkGroups = parseLinkGroups(r.typ, r.link)

	return kd
}

// compileSpecialCase compiles the SpecialCase column into an own-entry consistency
// constraint. Per-type-alternative groups must agree: the column compiles only when exactly
// one distinct non-empty group remains after deduplication ("[X];[]" and "[X];[X]" compile X,
// differing groups don't) -- safe because a constraint whose operands don't resolve against
// the value's actual shape evaluates as unknown, never as a flag. Everything uncompilable
// (fn:Ignore advisories, bit predicates, cross-object paths, domain predicates) is dropped;
// the column carries no predication accounting.
func compileSpecialCase(raw, rowName string) *condExpr {
	if raw == "" || strings.Contains(raw, "::") {
		return nil
	}
	distinct := ""
	for _, g := range splitGroups(raw) {
		if g == "" {
			continue
		}
		if distinct != "" && g != distinct {
			return nil
		}
		distinct = g
	}
	if distinct == "" {
		return nil
	}
	res := compileCond(distinct)
	if !res.ok || res.tree == nil {
		return nil
	}
	fillBitKeys(res.tree, rowName)
	if !condOperandsResolvable(res.tree, rowName) {
		return nil
	}
	return res.tree
}

// fillBitKeys anchors keyless bit-predicate leaves to the row's own key -- fn:BitsClear and
// friends implicitly test the value they annotate.
func fillBitKeys(c *condExpr, rowName string) {
	if (c.op == "CondBitsClear" || c.op == "CondBitsSet") && c.key == "" {
		c.key = rowName
	}
	for i := range c.kids {
		fillBitKeys(&c.kids[i], rowName)
	}
}

// parseIndirectReference resolves the IndirectReference column to a single IndirectRule.
// The column is a flat scalar (TRUE/FALSE/fn:...) for single-type keys, or bracket-grouped
// per type alternative ("[FALSE];[TRUE]") for multi-type keys; an fn: predicate is always
// flat regardless of type count. FALSE means the value need not be indirect (both direct and
// indirect are legal), not that it must be direct -- that is a separate, always-predicated
// fn:MustBeDirect() column value. So only an all-TRUE column resolves to IndirectRequired;
// every other case (including all-FALSE, and mixed TRUE/FALSE across type alternatives) is
// unconstrained.
func parseIndirectReference(raw string) string {
	if strings.Contains(raw, "fn:") {
		return "IndirectEither"
	}
	groups := splitGroups(raw)
	if len(groups) == 0 {
		return "IndirectEither"
	}
	allTrue := true
	for _, g := range groups {
		if g != "TRUE" {
			allTrue = false
			break
		}
	}
	if allTrue {
		return "IndirectRequired"
	}
	return "IndirectEither"
}

// splitGroups splits a possibly bracket-grouped, ";"-joined column ("[a];[b]") into its
// per-group contents ("a", "b"), or returns the raw value as a single group when unbracketed.
func splitGroups(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ";")
	out := make([]string, len(parts))
	for i, p := range parts {
		p = strings.TrimPrefix(p, "[")
		p = strings.TrimSuffix(p, "]")
		out[i] = p
	}
	return out
}

// pinnedExpr is the codegen form of one fn:RequiredValue entry: a compiled condition and the
// value the key must have while it holds.
type pinnedExpr struct {
	cond  condExpr
	value string
}

// parsePossibleValues flattens the (possibly per-type-grouped) PossibleValues column into a
// single enumerated list. Version-gated entries (fn:SinceVersion/fn:IsPDFVersion/
// fn:BeforeVersion wrapping a plain value) are folded against modelVersion; fn:Deprecated and
// fn:Extension entries stay legal (deprecated/extension values still occur in real files --
// the false-negative direction). fn:RequiredValue(cond, v) keeps v legal and, on a
// single-group column with a compilable condition, additionally emits a pin -- an
// uncompilable condition just loses the pin, never the entry. unresolved reports whether any
// entry carried a predicate this generator could not fold (e.g. a whole-group range
// constraint like fn:Eval(@LW>=0)), in which case the row must stay predicated. A literal "*"
// entry means any value is legal, so the whole list enforces nothing and is dropped -- along
// with any unresolved entry in it.
func parsePossibleValues(raw string) (values []string, pins []pinnedExpr, unresolved bool) {
	if raw == "" {
		return nil, nil, false
	}
	groups := splitGroups(raw)
	for _, group := range groups {
		for _, item := range splitTopLevelComma(group) {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if strings.HasPrefix(item, "fn:") {
				if name, args, wrapped := splitPredicate(item); wrapped && name == "fn:RequiredValue" && len(args) == 2 {
					v := strings.Trim(args[1], "'")
					if strings.Contains(args[1], "fn:") || v == "*" {
						unresolved = true
						continue
					}
					values = append(values, v)
					if len(groups) == 1 {
						if res := compileCond(args[0]); res.ok && res.tree != nil {
							pins = append(pins, pinnedExpr{cond: *res.tree, value: v})
						}
					}
					continue
				}
				val, keep, ok := foldValueEntry(item)
				if !ok {
					unresolved = true
					continue
				}
				if !keep {
					continue
				}
				item = val
			}
			item = strings.Trim(item, "'")
			if item == "*" {
				return nil, nil, false
			}
			values = append(values, item)
		}
	}
	return values, pins, unresolved
}

// foldValueEntry resolves one fn:-wrapped PossibleValues entry against modelVersion: keep
// reports whether the wrapped value is legal at modelVersion, ok whether the entry was fully
// resolved. A version gate that is false at modelVersion resolves to "omit" even when its
// payload carries a nested predicate; a kept payload must be a plain value.
func foldValueEntry(item string) (val string, keep, ok bool) {
	name, args, wrapped := splitPredicate(item)
	if !wrapped || len(args) != 2 {
		return "", false, false
	}
	plain := !strings.Contains(args[1], "fn:")
	switch name {
	case "fn:Deprecated", "fn:Extension":
		return args[1], true, plain
	case "fn:SinceVersion", "fn:BeforeVersion", "fn:IsPDFVersion":
		cmp, verOK := versionCmp(modelVersion, args[0])
		if !verOK {
			return "", false, false
		}
		var gate bool
		switch name {
		case "fn:SinceVersion":
			gate = cmp >= 0
		case "fn:BeforeVersion":
			gate = cmp < 0
		case "fn:IsPDFVersion":
			gate = cmp == 0
		}
		if !gate {
			return "", false, true
		}
		return args[1], true, plain
	}
	return "", false, false
}

// condExpr is the generator's form of an arlington.Cond.
type condExpr struct {
	op         string // Cond* identifier
	key, value string
	fn         string // Fn* identifier deriving the left operand ("" for the value itself)
	rhsKey     string // right operand is another entry's value instead of the value literal
	rhsFn      string
	rhsAdd     int    // affine right side: rhsAdd + rhsMul*op(rhsKey) - value(rhsKey2)
	rhsMul     int    // 0 means 1
	rhsKey2    string // subtracted second entry (plain value only)
	mod        int    // nonzero for "(<operand> mod N) == value" comparisons
	bitLo      int    // 1-based inclusive bit range for the CondBits* ops
	bitHi      int
	kids       []condExpr
}

// condResult is compileCond's outcome: a constant, a runtime condition tree, a vacuous
// term (a payload version gate whose gate is false -- the constraint does not apply, so it
// is neutral in whatever boolean context encloses it), or not ok (the expression depends on
// state this compiler cannot express).
type condResult struct {
	ok       bool
	isConst  bool
	constVal bool
	vacuous  bool
	tree     *condExpr
}

func constCond(v bool) condResult    { return condResult{ok: true, isConst: true, constVal: v} }
func treeCond(t condExpr) condResult { return condResult{ok: true, tree: &t} }

// compileRequired compiles a Required column of the form fn:IsRequired(expr). A vacuous
// condition means the requirement does not apply.
func compileRequired(raw string) condResult {
	name, args, wrapped := splitPredicate(strings.TrimSpace(raw))
	if !wrapped || name != "fn:IsRequired" || len(args) != 1 {
		return condResult{}
	}
	res := compileCond(args[0])
	if res.vacuous {
		return constCond(false)
	}
	return res
}

// foldIndirect constant-folds an IndirectReference column of the form fn:MustBeIndirect(expr)
// with a pure version-gate condition (empty means unconditionally indirect). fn:MustBeDirect
// is not folded here: a true result needs the IndirectForbidden disposition, which no check
// consumes.
func foldIndirect(raw string) (rule string, ok bool) {
	name, args, wrapped := splitPredicate(strings.TrimSpace(raw))
	if !wrapped || name != "fn:MustBeIndirect" || len(args) > 1 {
		return "", false
	}
	must := true
	if len(args) == 1 && args[0] != "" {
		res := compileCond(args[0])
		if !res.ok || !res.isConst {
			return "", false
		}
		must = res.constVal
	}
	if must {
		return "IndirectRequired", true
	}
	return "IndirectEither", true
}

// compileCond compiles a predicate expression into a constant (version primitives fold
// against modelVersion) or a runtime condition over the owning container's own entries:
// fn:IsPresent and fn:Contains of a sibling key, comparisons between operands (@Key,
// fn:ArrayLength/fn:StringLength of a key, optionally "mod N") and a literal or second
// operand, fn:Extension gates as CondUnknown -- combined with fn:Not, parentheses, and
// ||/&&. Anything else -- cross-object paths (::), parent::, domain predicates -- is not ok,
// and the caller must keep the column predicated.
func compileCond(expr string) condResult {
	expr = trimOuterParens(strings.TrimSpace(expr))

	if parts := splitTopLevelOp(expr, "||"); len(parts) > 1 {
		return compileBool(parts, false)
	}
	if parts := splitTopLevelOp(expr, "&&"); len(parts) > 1 {
		return compileBool(parts, true)
	}

	if c, ok := splitComparison(expr); ok {
		return treeCond(c)
	}

	name, args, wrapped := splitPredicate(expr)
	if !wrapped {
		return condResult{}
	}
	switch name {
	case "fn:Not":
		if len(args) != 1 {
			return condResult{}
		}
		res := compileCond(args[0])
		switch {
		case !res.ok:
			return condResult{}
		case res.vacuous:
			return res // an inapplicable constraint stays inapplicable under negation
		case res.isConst:
			return constCond(!res.constVal)
		default:
			return treeCond(condExpr{op: "CondNot", kids: []condExpr{*res.tree}})
		}
	case "fn:IsPresent":
		if len(args) == 1 && (plainKey(args[0]) || indexKey(args[0])) {
			return treeCond(condExpr{op: "CondPresent", key: args[0]})
		}
		return condResult{}
	case "fn:Contains":
		if len(args) == 2 && strings.HasPrefix(args[0], "@") && plainValue(args[1]) {
			if k := args[0][1:]; plainKey(k) || indexKey(k) {
				return treeCond(condExpr{op: "CondContains", key: k, value: args[1]})
			}
		}
		return condResult{}
	case "fn:NotStandard14Font":
		// The predicate reads the owning font dict's own BaseFont, so it is an own-entry
		// condition despite its domain-function spelling.
		if len(args) == 0 {
			return treeCond(condExpr{op: "CondNotStd14", key: "BaseFont"})
		}
		return condResult{}
	case "fn:BitClear", "fn:BitSet", "fn:BitsClear", "fn:BitsSet":
		// Bit predicates implicitly test the row's own key; compileSpecialCase fills the
		// key in afterwards (they occur only in that column), so the leaf compiles keyless
		// here and condOperandsResolvable rejects any that were never patched.
		op := "CondBitsClear"
		if name == "fn:BitSet" || name == "fn:BitsSet" {
			op = "CondBitsSet"
		}
		lo, hi, ok := parseBitRange(name, args)
		if !ok {
			return condResult{}
		}
		return treeCond(condExpr{op: op, bitLo: lo, bitHi: hi})
	case "fn:Eval":
		if len(args) != 1 {
			return condResult{}
		}
		return compileCond(args[0])
	case "fn:Extension":
		// Whether an extension is in play is unknowable here, so the term compiles to a
		// CondUnknown leaf: a decisive sibling operand can still settle the enclosing
		// And/Or, otherwise the runtime skips the check -- the false-negative direction.
		if len(args) == 1 || len(args) == 2 {
			return treeCond(condExpr{op: "CondUnknown"})
		}
		return condResult{}
	case "fn:SinceVersion", "fn:BeforeVersion", "fn:IsPDFVersion":
		if len(args) == 0 || len(args) > 2 {
			return condResult{}
		}
		cmp, verOK := versionCmp(modelVersion, args[0])
		if !verOK {
			return condResult{}
		}
		var gate bool
		switch name {
		case "fn:SinceVersion":
			gate = cmp >= 0
		case "fn:BeforeVersion":
			gate = cmp < 0
		case "fn:IsPDFVersion":
			gate = cmp == 0
		}
		if !gate {
			if len(args) == 2 {
				// The gated constraint does not apply at modelVersion: neutral, not false.
				return condResult{ok: true, vacuous: true}
			}
			return constCond(false)
		}
		if len(args) == 1 {
			return constCond(true)
		}
		res := compileCond(args[1])
		if res.ok && res.tree != nil && treeHasBitLeaf(*res.tree) {
			// A version-gated bit rule means those bits are defined in some other PDF
			// version; real files carry such flags harmlessly and no PDF/A validator
			// objects (the KeyIntroducedAfterPDF14 precedent), so the payload is dropped.
			// Unconditional bit rules (reserved-forever bits) still compile.
			return condResult{ok: true, vacuous: true}
		}
		return res
	}
	return condResult{}
}

// treeHasBitLeaf reports whether any leaf of c is a bit predicate.
func treeHasBitLeaf(c condExpr) bool {
	if c.bitLo != 0 {
		return true
	}
	for _, k := range c.kids {
		if treeHasBitLeaf(k) {
			return true
		}
	}
	return false
}

// compileBool combines the operands of a top-level || (and=false) or && (and=true): one
// decisive constant operand (true for ||, false for &&) settles the whole expression even if
// siblings are uncompilable; neutral constants drop out; the remaining runtime trees combine.
func compileBool(parts []string, and bool) condResult {
	var kids []condExpr
	anyBad := false
	for _, p := range parts {
		res := compileCond(p)
		switch {
		case res.ok && res.vacuous:
			// an inapplicable constraint is neutral in either context, drop
		case res.ok && res.isConst && res.constVal != and:
			return constCond(!and)
		case res.ok && res.isConst:
			// neutral operand, drop
		case res.ok:
			kids = append(kids, *res.tree)
		default:
			anyBad = true
		}
	}
	if anyBad {
		return condResult{}
	}
	switch len(kids) {
	case 0:
		return constCond(and)
	case 1:
		return treeCond(kids[0])
	}
	op := "CondOr"
	if and {
		op = "CondAnd"
	}
	return treeCond(condExpr{op: op, kids: kids})
}

// splitComparison recognizes a top-level "@Key <op> literal" comparison, where <op> is one
// of ==, !=, >=, <=, > or <, and the modulo form "(@Key mod N) ==/!= literal".
func splitComparison(expr string) (c condExpr, ok bool) {
	depth := 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '=', '!', '<', '>':
			if depth != 0 {
				continue
			}
			width := 1
			var op string
			switch {
			case i+1 < len(expr) && expr[i+1] == '=':
				width = 2
				op = map[byte]string{'=': "CondEq", '!': "CondNe", '<': "CondLe", '>': "CondGe"}[expr[i]]
			case expr[i] == '<':
				op = "CondLt"
			case expr[i] == '>':
				op = "CondGt"
			default:
				return condExpr{}, false // bare '=' or '!'
			}
			lhs, rhs := strings.TrimSpace(expr[:i]), strings.TrimSpace(expr[i+width:])
			c, lhsOK := splitModOperand(lhs)
			if !lhsOK {
				return condExpr{}, false
			}
			c.op = op
			switch {
			case plainValue(rhs):
				c.value = rhs
			case c.mod != 0:
				return condExpr{}, false // modulo compares against a literal only
			default:
				if rk, rfn, rhsOK := parseOperand(trimOuterParens(rhs)); rhsOK {
					c.rhsKey, c.rhsFn = rk, rfn
				} else if aff, affOK := parseAffineRHS(rhs); affOK {
					c.rhsKey, c.rhsFn = aff.rhsKey, aff.rhsFn
					c.rhsAdd, c.rhsMul, c.rhsKey2 = aff.rhsAdd, aff.rhsMul, aff.rhsKey2
				} else {
					return condExpr{}, false
				}
			}
			if c.mod != 0 && op != "CondEq" && op != "CondNe" {
				return condExpr{}, false // modulo only pins equality; anything else fails closed
			}
			return c, true
		}
	}
	return condExpr{}, false
}

// splitModOperand parses a comparison's left side: an operand, or the modulo form
// "(<operand> mod N)" with a positive integer divisor (mod 0 when absent).
func splitModOperand(lhs string) (c condExpr, ok bool) {
	lhs = trimOuterParens(lhs)
	if before, after, found := strings.Cut(lhs, " mod "); found {
		n, err := strconv.Atoi(strings.TrimSpace(after))
		if err != nil || n <= 0 {
			return condExpr{}, false
		}
		lhs, c.mod = strings.TrimSpace(before), n
	}
	c.key, c.fn, ok = parseOperand(lhs)
	return c, ok
}

// parseAffineRHS recognizes a comparison's arithmetic right side: an integer literal
// combined with an operand by one top-level + / - / * ("1+(@LastChar - @FirstChar)",
// "2 * @N", "fn:ArrayLength(Bounds)+1"), where the operand may itself be the difference of
// two plain entries. Division, literal-minus-operand, and deeper arithmetic fail closed.
func parseAffineRHS(s string) (condExpr, bool) {
	lhs, op, rhs, ok := splitTopLevelArith(trimOuterParens(strings.TrimSpace(s)))
	if !ok {
		return condExpr{}, false
	}
	ln, lErr := strconv.Atoi(lhs)
	rn, rErr := strconv.Atoi(rhs)
	switch {
	case op == '+' && lErr == nil:
		return affineOperand(rhs, ln, 0)
	case op == '+' && rErr == nil:
		return affineOperand(lhs, rn, 0)
	case op == '-' && rErr == nil:
		return affineOperand(lhs, -rn, 0)
	case op == '-' && lErr != nil:
		return affineDiff(lhs, rhs, 0)
	case op == '*' && lErr == nil:
		return affineOperand(rhs, 0, ln)
	case op == '*' && rErr == nil:
		return affineOperand(lhs, 0, rn)
	}
	return condExpr{}, false
}

// affineOperand parses the non-literal side of an affine expression: a plain or derived
// operand, or -- only unscaled -- the difference of two plain entries.
func affineOperand(s string, add, mul int) (condExpr, bool) {
	s = trimOuterParens(strings.TrimSpace(s))
	if k, fn, ok := parseOperand(s); ok {
		return condExpr{rhsKey: k, rhsFn: fn, rhsAdd: add, rhsMul: mul}, true
	}
	if mul != 0 {
		return condExpr{}, false // a scaled difference does not occur in the model
	}
	if l, op, r, ok := splitTopLevelArith(s); ok && op == '-' {
		return affineDiff(l, r, add)
	}
	return condExpr{}, false
}

// affineDiff parses "@A - @B": a derived-or-plain minuend and a plain-value subtrahend.
func affineDiff(lhs, rhs string, add int) (condExpr, bool) {
	lk, lfn, lok := parseOperand(lhs)
	rk, rfn, rok := parseOperand(rhs)
	if !lok || !rok || rfn != "" {
		return condExpr{}, false
	}
	return condExpr{rhsKey: lk, rhsFn: lfn, rhsAdd: add, rhsKey2: rk}, true
}

// splitTopLevelArith splits s at its first top-level binary + / - / * (a '-' with nothing
// before it is a sign, not a split point).
func splitTopLevelArith(s string) (lhs string, op byte, rhs string, ok bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '(':
			depth++
		case ')':
			depth--
		case '+', '-', '*':
			if depth != 0 || (c == '-' && strings.TrimSpace(s[:i]) == "") {
				continue
			}
			return strings.TrimSpace(s[:i]), c, strings.TrimSpace(s[i+1:]), true
		}
	}
	return "", 0, "", false
}

// parseBitRange resolves a bit predicate's arguments to a 1-based inclusive bit range:
// one bit position for the singular forms, lo..hi for the plural ones.
func parseBitRange(name string, args []string) (lo, hi int, ok bool) {
	single := name == "fn:BitClear" || name == "fn:BitSet"
	if (single && len(args) != 1) || (!single && len(args) != 2) {
		return 0, 0, false
	}
	lo, err := strconv.Atoi(args[0])
	if err != nil {
		return 0, 0, false
	}
	hi = lo
	if !single {
		if hi, err = strconv.Atoi(args[1]); err != nil {
			return 0, 0, false
		}
	}
	if lo < 1 || hi < lo || hi > 64 {
		return 0, 0, false
	}
	return lo, hi, true
}

// parseOperand recognizes a comparison operand: "@Key", or a derived quantity
// "fn:ArrayLength(Key)" / "fn:StringLength(Key)" (the argument may carry an @ prefix).
// fn is the Go Fn* identifier, empty for a plain value operand.
func parseOperand(s string) (key, fn string, ok bool) {
	if strings.HasPrefix(s, "@") {
		if k := s[1:]; plainKey(k) || indexKey(k) {
			return k, "", true
		}
		return "", "", false
	}
	name, args, wrapped := splitPredicate(s)
	if !wrapped || len(args) != 1 {
		return "", "", false
	}
	switch name {
	case "fn:ArrayLength":
		fn = "FnArrayLength"
	case "fn:StringLength":
		fn = "FnStringLength"
	default:
		return "", "", false
	}
	k := strings.TrimPrefix(args[0], "@")
	if plainKey(k) || indexKey(k) {
		return k, fn, true
	}
	return "", "", false
}

// plainKey reports whether s is a bare sibling key name (no path, no operators). All-digit
// names are array-index references (@0, @1), which have no dictionary to resolve against.
func plainKey(s string) bool {
	if s == "" {
		return false
	}
	hasLetter := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_':
			hasLetter = true
		case c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return hasLetter
}

// indexKey reports whether s is a fixed array-index reference (@0, @1): all digits.
func indexKey(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// condOperandsResolvable reports whether every leaf operand of tree can be resolved from the
// row it belongs to -- sibling key names for a named dict row, element indices for a fixed
// array-index row -- and the tree observes at least one real operand (a tree of only
// CondUnknown leaves enforces nothing, so its row stays honestly predicated). A nil tree
// (vacuous constraint) is resolvable for either. Wildcard and offset-wildcard rows resolve
// nothing.
func condOperandsResolvable(tree *condExpr, rowName string) bool {
	var want func(string) bool
	switch {
	case plainKey(rowName):
		want = plainKey
	case indexKey(rowName):
		want = indexKey
	default:
		return false
	}
	operands := 0
	var walk func(c condExpr) bool
	walk = func(c condExpr) bool {
		if c.bitLo != 0 && c.key == "" {
			return false // a bit predicate outside SpecialCase has no key to test
		}
		for _, k := range []string{c.key, c.rhsKey, c.rhsKey2} {
			if k == "" {
				continue
			}
			if !want(k) {
				return false
			}
			operands++
		}
		for _, k := range c.kids {
			if !walk(k) {
				return false
			}
		}
		return true
	}
	return tree == nil || (walk(*tree) && operands > 0)
}

// plainValue reports whether s is a bare comparison literal (name/number token).
func plainValue(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '.' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// splitPredicate splits "fn:Name(arg0,arg1)" into its name and top-level comma-separated
// arguments; wrapped is false when expr is not a single fn: call spanning the whole string.
func splitPredicate(expr string) (name string, args []string, wrapped bool) {
	if !strings.HasPrefix(expr, "fn:") {
		return "", nil, false
	}
	open := strings.IndexByte(expr, '(')
	if open < 0 || !strings.HasSuffix(expr, ")") {
		return "", nil, false
	}
	inner := expr[open+1 : len(expr)-1]
	depth := 0
	for _, c := range inner {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return "", nil, false // the closing paren is not this call's
			}
		}
	}
	if depth != 0 {
		return "", nil, false
	}
	if inner == "" {
		return expr[:open], nil, true
	}
	for _, a := range splitTopLevelComma(inner) {
		args = append(args, strings.TrimSpace(a))
	}
	return expr[:open], args, true
}

// splitTopLevelOp splits expr on a two-character boolean operator at paren depth 0.
func splitTopLevelOp(expr, op string) []string {
	var parts []string
	depth, start := 0, 0
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '(':
			depth++
		case ')':
			depth--
		default:
			if depth == 0 && strings.HasPrefix(expr[i:], op) {
				parts = append(parts, strings.TrimSpace(expr[start:i]))
				i += len(op) - 1
				start = i + 1
			}
		}
	}
	parts = append(parts, strings.TrimSpace(expr[start:]))
	return parts
}

// trimOuterParens strips one pair of parentheses when they enclose the whole expression.
func trimOuterParens(expr string) string {
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") {
		depth := 0
		for i := 0; i < len(expr); i++ {
			switch expr[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 && i != len(expr)-1 {
					return expr // e.g. "(a) && (b)" -- the outer parens don't match
				}
			}
		}
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	return expr
}

// versionCmp numerically compares two "major.minor" PDF version strings; ok is false when
// either operand does not have that shape.
func versionCmp(a, b string) (cmp int, ok bool) {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return 0, false
	}
	if pa != pb {
		if pa < pb {
			return -1, true
		}
		return 1, true
	}
	return 0, true
}

// parseVersion converts "1.4" into a single comparable number (major*100+minor).
func parseVersion(s string) (int, bool) {
	major, minor, found := strings.Cut(s, ".")
	if !found || len(major) != 1 || len(minor) != 1 ||
		major[0] < '0' || major[0] > '9' || minor[0] < '0' || minor[0] > '9' {
		return 0, false
	}
	return int(major[0]-'0')*100 + int(minor[0]-'0'), true
}

// parseLinkGroups splits typ and link in lockstep, one linkGroup per Type alternative, so a
// key's runtime value (once its Go kind is known) can be resolved to the right subset of
// candidate Arlington types. With a single Type alternative, or when the two columns'
// alternative counts don't line up (a shape this generator doesn't expect), it returns one
// group with no kind condition that covers every candidate. It never guesses which
// alternative a candidate belongs to.
func parseLinkGroups(typ, link string) []linkGroup {
	if link == "" {
		return nil
	}
	linkParts := splitGroups(link)
	typeToks := strings.Split(typ, ";")

	if len(typeToks) <= 1 || len(typeToks) != len(linkParts) {
		var candidates []string
		for _, part := range linkParts {
			candidates = append(candidates, splitNames(part)...)
		}
		if len(candidates) == 0 {
			return nil
		}
		return []linkGroup{{candidates: candidates}}
	}

	var groups []linkGroup
	for i, tok := range typeToks {
		candidates := splitNames(linkParts[i])
		if len(candidates) == 0 {
			continue // this alternative has no sub-schema (e.g. a plain scalar type)
		}
		ident, ok := valueTypeIdent[tok]
		if !ok {
			continue // an unrecognized/predicated type token can't be gated on kind; skip it
		}
		groups = append(groups, linkGroup{valueTypes: []string{ident}, candidates: candidates})
	}
	return groups
}

// splitNames splits one bracket-group's comma-separated Arlington type names.
func splitNames(group string) []string {
	var out []string
	for _, item := range strings.Split(group, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

// resolveDiscriminators fills in each of kd's LinkGroups' discriminator, if any group has more
// than one candidate. keyDefsByType looks up each candidate's own schema.
func resolveDiscriminators(kd *keyDef, keyDefsByType map[string][]keyDef) {
	for gi := range kd.linkGroups {
		g := &kd.linkGroups[gi]
		if len(g.candidates) < 2 {
			continue
		}
		key, byValue := bestDiscriminator(g.candidates, keyDefsByType)
		if len(byValue) == 0 {
			continue
		}
		g.discriminator = key
		g.byValue = byValue
	}
}

// bestDiscriminator finds the key name that most cleanly disambiguates candidates: a key that
// some candidates declare as Required, non-Predicated, with exactly one PossibleValues entry
// (e.g. AnnotText.Subtype -> [Text], ActionGoTo.S -> [GoTo], FunctionType2.FunctionType -> [2]).
// A candidate that doesn't qualify for the chosen key, or whose value collides with another
// candidate's, is simply absent from the returned map -- partial coverage is safe, since an
// unresolvable candidate is never guessed, just never selected at runtime. Ties (equal
// candidate coverage) break alphabetically by key name, for deterministic generator output.
func bestDiscriminator(candidates []string, keyDefsByType map[string][]keyDef) (string, map[string]string) {
	qualifying := map[string]map[string]string{} // candidate type -> key name -> its one possible value
	keyNameSet := map[string]bool{}
	for _, c := range candidates {
		q := map[string]string{}
		for _, kd := range keyDefsByType[c] {
			if kd.required && !kd.predicated.required && !kd.predicated.values && len(kd.possibleValues) == 1 {
				q[kd.name] = kd.possibleValues[0]
				keyNameSet[kd.name] = true
			}
		}
		qualifying[c] = q
	}

	var keyNames []string
	for k := range keyNameSet {
		keyNames = append(keyNames, k)
	}
	sort.Strings(keyNames)

	var bestKey string
	var bestMap map[string]string
	for _, k := range keyNames {
		byValue := map[string]string{}
		collided := map[string]bool{}
		for _, c := range candidates {
			v, ok := qualifying[c][k]
			if !ok {
				continue
			}
			if existing, taken := byValue[v]; taken && existing != c {
				collided[v] = true
				continue
			}
			byValue[v] = c
		}
		for v := range collided {
			delete(byValue, v)
		}
		if len(byValue) > len(bestMap) {
			bestKey, bestMap = k, byValue
		}
	}
	return bestKey, bestMap
}

// splitTopLevelComma splits s on commas that are not nested inside parentheses, so that
// predicate expressions like "fn:RequiredValue(@V<2,2)" survive as one token.
func splitTopLevelComma(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}
