package pdf

import (
	"slices"
	"strings"
	"testing"
)

// stableCheckIDs pins every check's registration ID. IDs key Profile's
// enabled set and are public through Check.ID, so inserting a check for a new
// level must not renumber the existing ones: a new check takes a new ID.
var stableCheckIDs = map[string]int{
	"FileHeaderSignature":             1,
	"FileHeaderComment":               2,
	"FileHeaderCommentLength":         3,
	"FileHeaderCommentBytes":          4,
	"TrailerID":                       5,
	"TrailerEncrypt":                  6,
	"TrailerEOF":                      7,
	"XRefKeyword":                     8,
	"XRefSubsectionHeader":            9,
	"XRefSubsectionHeaderFormat":      10,
	"XRefStream":                      11,
	"InfoDictUnreadable":              12,
	"InfoDictEmptyValues":             13,
	"InfoDictXMPMismatch":             14,
	"GraphResolutionFailure":          15,
	"HexStringInvalidChar":            16,
	"HexStringOddLength":              17,
	"StreamFileSpec":                  18,
	"StreamFileFilter":                19,
	"StreamFileDecodeParams":          20,
	"StreamKeywordEOL":                21,
	"EndstreamEOL":                    22,
	"StreamLengthIncludesEOL":         23,
	"StreamLengthMismatch":            24,
	"StreamUndecodable":               25,
	"ObjectFraming":                   26,
	"StreamLZWFilter":                 27,
	"InlineImageLZWFilter":            28,
	"EmbeddedFileSpec":                29,
	"EmbeddedFiles":                   30,
	"NameTooLong":                     31,
	"IntegerOutOfRange":               32,
	"RealOutOfRange":                  33,
	"ArrayTooLarge":                   34,
	"DictTooLarge":                    35,
	"CMapCIDOutOfRange":               36,
	"StringTooLong":                   37,
	"DeviceNColorants":                38,
	"IndirectObjectsExceeded":         39,
	"GraphicsStateNesting":            40,
	"OptionalContent":                 41,
	"PostPDF14ViewerPref":             42,
	"OutputIntentNotArray":            43,
	"OutputIntentNotDict":             44,
	"OutputIntentInvalidS":            45,
	"OutputIntentWrongS":              46,
	"OutputIntentMissingIdentifier":   47,
	"OutputIntentMultipleProfiles":    48,
	"OutputIntentUnresolvedProfile":   49,
	"OutputIntentInvalidProfile":      50,
	"OutputIntentMissingN":            51,
	"OutputIntentInvalidN":            52,
	"OutputIntentICCVersion":          53,
	"ICCBasedComponentsMismatch":      54,
	"ICCBasedProfileInvalid":          55,
	"DeviceColourSpaceUsage":          56,
	"DeviceColourContentStream":       57,
	"SeparationAlternateColour":       58,
	"RenderingIntent":                 59,
	"UndefinedOperator":               60,
	"ImageInterpolate":                61,
	"ImageAlternates":                 62,
	"ImageOPI":                        63,
	"ImageRenderingIntent":            64,
	"ImageBitsPerComponent":           65,
	"ImageMaskBitsPerComponent":       66,
	"FormOPI":                         67,
	"FormSubtype2PS":                  68,
	"FormPSEntry":                     69,
	"ReferenceXObject":                70,
	"FormPostScript":                  71,
	"PostScriptXObject":               72,
	"TransferFunction":                73,
	"DefaultTransferFunction":         74,
	"ExtGStateRenderingIntent":        75,
	"SoftMaskExtGState":               76,
	"BlendMode":                       77,
	"StrokingAlpha":                   78,
	"NonStrokingAlpha":                79,
	"TransparencyGroup":               80,
	"ImageWithSoftMask":               81,
	"FontType":                        82,
	"InvalidSubtype":                  83,
	"FontBaseFont":                    84,
	"SimpleFontFirstChar":             85,
	"SimpleFontLastChar":              86,
	"SimpleFontWidths":                87,
	"FontFileSubtype":                 88,
	"InvalidProgram":                  89,
	"CIDSystemInfoMismatch":           90,
	"CIDToGIDMapMissing":              91,
	"CMapNotEmbedded":                 92,
	"CMapWModeInconsistent":           93,
	"SimpleNotEmbedded":               94,
	"CIDNotEmbedded":                  95,
	"SubsetGlyphCoverage":             96,
	"Type1SubsetCharSet":              97,
	"CIDSubsetCIDSet":                 98,
	"AdvanceWidthMismatch":            99,
	"TrueTypeEncoding":                100,
	"SymbolicTrueTypeEncoding":        101,
	"SymbolicTrueTypeCmap":            102,
	"ToUnicodeMissing":                103,
	"TaggedMarkInfo":                  104,
	"StructTreeRoot":                  105,
	"RoleMapStandardType":             106,
	"RoleMapCircular":                 107,
	"LangIdentifier":                  108,
	"DisallowedSubtype":               109,
	"PrintFlagNotSet":                 110,
	"HiddenFlagSet":                   111,
	"InvisibleFlagSet":                112,
	"NoViewFlagSet":                   113,
	"OpacityNotOne":                   114,
	"MissingAppearance":               115,
	"AppearanceMissingN":              116,
	"AppearanceExtraEntries":          117,
	"AppearanceNNotStream":            118,
	"ColourWithoutIntent":             119,
	"ForbiddenActionType":             120,
	"DisallowedNamedAction":           121,
	"AdditionalActions":               122,
	"MetadataMissing":                 123,
	"MetadataFiltered":                124,
	"MetadataPropertyType":            125,
	"MetadataUndeclaredProperty":      126,
	"InfoXMPSync":                     127,
	"XPacketBytesAttribute":           128,
	"XPacketEncodingAttribute":        129,
	"ObjectXMPNoXPacket":              130,
	"ExtSchemaNamespace":              131,
	"ExtSchemaWrongPrefixURI":         132,
	"ExtSchemasNotBag":                133,
	"ExtPropertyMultipleName":         134,
	"ExtPropertyMissingField":         135,
	"ExtPropertyComplexAsSimple":      136,
	"ExtTypeInvalid":                  137,
	"ExtFieldInvalid":                 138,
	"ExtPropertyUndocumented":         139,
	"ExtPropertyUndefinedType":        140,
	"XMPStreamUnreadable":             141,
	"XMPNotWellFormed":                142,
	"XMPNoCorrespondingType":          143,
	"PDFAIdentifierMissing":           144,
	"PDFAIdentifierNamespace":         145,
	"PDFAConformanceLevel":            146,
	"PDFAPartNumber":                  147,
	"PDFAIdentifierUndefinedProperty": 148,
	"NeedAppearances":                 149,
	"XFA":                             150,
	"FieldAction":                     151,
	"FieldAdditionalActions":          152,
	"WidgetMissingAppearance":         153,
	"MissingRequiredKey":              154,
	"WrongValueType":                  155,
	"DisallowedValue":                 156,
	"IndirectRequired":                157,
	"KeyIntroducedAfterPDF14":         158,
	"ConstraintViolated":              159,
	"NameNotUTF8":                     160,
	"PermissionsKeys":                 161,
	"SignatureReferenceDigest":        162,
	"PageBoundarySize":                163,
}

func TestCheckIDsAreStable(t *testing.T) {
	all := AllChecks()
	if len(all) != len(stableCheckIDs) {
		t.Errorf("catalog has %d checks, stableCheckIDs pins %d; pin the new check's ID", len(all), len(stableCheckIDs))
	}
	for _, c := range all {
		if want, ok := stableCheckIDs[c.Name()]; !ok {
			t.Errorf("check %s has no pinned ID", c.Name())
		} else if c.ID() != want {
			t.Errorf("check %s: ID %d, want %d", c.Name(), c.ID(), want)
		}
	}
}

// TestNewCheckRejectsDuplicateID verifies that registering a check with an ID
// already in use panics, since two checks sharing an ID would share one slot in
// every Profile.
func TestNewCheckRejectsDuplicateID(t *testing.T) {
	existing := AllChecks()[0]
	savedLen := len(allChecksCatalog)
	defer func() {
		if r := recover(); r == nil {
			t.Error("newCheck did not panic on a duplicate ID")
		}
		if len(allChecksCatalog) != savedLen {
			t.Errorf("catalog grew from %d to %d on a rejected registration", savedLen, len(allChecksCatalog))
		}
	}()
	newCheck(existing.ID(), "DuplicateID", "duplicates "+existing.Name(), "99.99", 1)
}

func TestClauseIn(t *testing.T) {
	for _, tc := range []struct {
		name   string
		check  Check
		level  LevelType
		clause string
		sub    int
		ok     bool
	}{
		{"1b check under A1B", Checks.Transparency.ImageWithSoftMask, A1B, "6.4", 6, true},
		{"1b check under ObjectModel", Checks.Transparency.ImageWithSoftMask, ObjectModel, "", 0, false},
		{"1b check under Undefined", Checks.Transparency.ImageWithSoftMask, Undefined, "", 0, false},
		{"1b check under the empty level", Checks.Transparency.ImageWithSoftMask, "", "", 0, false},
		{"1a-only check under A1B", Checks.Font.ToUnicodeMissing, A1B, "", 0, false},
		{"1a-only logical-structure check under A1B", Checks.LogicalStructure.TaggedMarkInfo, A1B, "", 0, false},
		{"object-model check under ObjectModel", Checks.ObjectModel.WrongValueType, ObjectModel, ObjectModelClause, 2, true},
		{"object-model check under A1B", Checks.ObjectModel.WrongValueType, A1B, ObjectModelClause, 2, true},
		{"object-model check under A2B", Checks.ObjectModel.WrongValueType, A2B, ObjectModelClause, 2, true},
		{"1b-only check under A2B", Checks.Transparency.ImageWithSoftMask, A2B, "", 0, false},
		{"unregistered check", Check{}, A1B, "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clause, sub, ok := tc.check.ClauseIn(tc.level)
			if clause != tc.clause || sub != tc.sub || ok != tc.ok {
				t.Errorf("ClauseIn(%q) = (%q, %d, %v), want (%q, %d, %v)", tc.level, clause, sub, ok, tc.clause, tc.sub, tc.ok)
			}
			if got := tc.check.AppliesTo(tc.level); got != tc.ok {
				t.Errorf("AppliesTo(%q) = %v, want %v", tc.level, got, tc.ok)
			}
		})
	}
}

// TestEveryPDFACheckAppliesToA1B verifies that, apart from the 1a-only checks
// and the rules of Part 2 only, the whole PDF/A catalog is Part 1 Level B,
// under the clause it was registered with.
func TestEveryPDFACheckAppliesToA1B(t *testing.T) {
	oneAOnly := map[Check]bool{
		Checks.Font.ToUnicodeMissing:                true,
		Checks.LogicalStructure.TaggedMarkInfo:      true,
		Checks.LogicalStructure.StructTreeRoot:      true,
		Checks.LogicalStructure.RoleMapStandardType: true,
		Checks.LogicalStructure.RoleMapCircular:     true,
		Checks.LogicalStructure.LangIdentifier:      true,
	}
	for _, c := range AllChecks() {
		clause, sub, ok := c.ClauseIn(A1B)
		if c.Clause() == "" {
			if ok {
				t.Errorf("%s: a rule of Part 2 only applies to A1B", c.Name())
			}
			continue
		}
		if ok == oneAOnly[c] {
			t.Errorf("%s: AppliesTo(A1B) = %v", c.Name(), ok)
			continue
		}
		if ok && (clause != c.Clause() || sub != c.Subclause()) {
			t.Errorf("%s: ClauseIn(A1B) = %s/%d, want %s/%d", c.Name(), clause, sub, c.Clause(), c.Subclause())
		}
		if c.AppliesTo(ObjectModel) != (c.Clause() == ObjectModelClause) {
			t.Errorf("%s: AppliesTo(ObjectModel) = %v", c.Name(), c.AppliesTo(ObjectModel))
		}
	}
}

func TestCheckByClauseIn(t *testing.T) {
	if c, ok := CheckByClauseIn(A1B, "6.3.4", 1); !ok || c != Checks.Font.SimpleNotEmbedded {
		t.Errorf("CheckByClauseIn(A1B, 6.3.4, 1) = %s, %v", c.Name(), ok)
	}
	if c, ok := CheckByClauseIn(ObjectModel, ObjectModelClause, 2); !ok || c != Checks.ObjectModel.WrongValueType {
		t.Errorf("CheckByClauseIn(ObjectModel, objmodel, 2) = %s, %v", c.Name(), ok)
	}
	for _, tc := range []struct {
		level  LevelType
		clause string
		sub    int
	}{
		{ObjectModel, "6.3.4", 1}, // a PDF/A clause is no clause of the object model
		{A1B, "6.3.8", 1},         // ToUnicode is a Level A requirement
		{A1B, "6.3.4", 99},
		{Undefined, "6.3.4", 1},
	} {
		if c, ok := CheckByClauseIn(tc.level, tc.clause, tc.sub); ok {
			t.Errorf("CheckByClauseIn(%s, %s, %d) = %s, want none", tc.level, tc.clause, tc.sub, c.Name())
		}
	}
}

func TestChecksForClauseIn(t *testing.T) {
	got := ChecksForClauseIn(A1B, "6.3.4")
	want := []Check{Checks.Font.SimpleNotEmbedded, Checks.Font.CIDNotEmbedded}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("ChecksForClauseIn(A1B, 6.3.4) = %v, want %v", got, want)
	}
	if got := ChecksForClauseIn(A1B, "6.3.8"); len(got) != 0 {
		t.Errorf("ChecksForClauseIn(A1B, 6.3.8) = %v, want none: ToUnicode is Level A", got)
	}
	if got := ChecksForClauseIn(ObjectModel, "6.3.4"); len(got) != 0 {
		t.Errorf("ChecksForClauseIn(ObjectModel, 6.3.4) = %v, want none", got)
	}
	if got := ChecksForClauseIn(ObjectModel, ObjectModelClause); len(got) != 6 {
		t.Errorf("ChecksForClauseIn(ObjectModel, objmodel) has %d checks, want 6", len(got))
	}
}

// TestPart2Clauses verifies that every Part 2 entry numbers a registered
// PDF/A check under a well-formed clause, that ClauseIn(A2B) and
// DescriptionIn(A2B) report the entry, and that a check applies to PDF/A-2b
// exactly when Part 2 numbers it or it is an object-model check.
func TestPart2Clauses(t *testing.T) {
	byName := map[string]Check{}
	for _, c := range AllChecks() {
		byName[c.Name()] = c
	}
	for name, ref := range part2Clauses {
		c, ok := byName[name]
		if !ok {
			t.Errorf("part2Clauses numbers %s, which no check is named", name)
			continue
		}
		if c.Clause() == ObjectModelClause {
			t.Errorf("%s: an object-model check keeps its clause under every level", c.Name())
		}
		if !strings.HasPrefix(ref.clause, "6.") || ref.subclause < 0 {
			t.Errorf("%s: Part 2 clause %q/%d", c.Name(), ref.clause, ref.subclause)
		}
		if clause, sub, ok := c.ClauseIn(A2B); !ok || clause != ref.clause || sub != ref.subclause {
			t.Errorf("%s: ClauseIn(A2B) = %s/%d, %v, want %s/%d", c.Name(), clause, sub, ok, ref.clause, ref.subclause)
		}
		want := ref.description
		if want == "" {
			want = c.Description()
		}
		if got := c.DescriptionIn(A2B); got != want {
			t.Errorf("%s: DescriptionIn(A2B) = %q, want %q", c.Name(), got, want)
		}
		if got := c.DescriptionIn(A1B); got != c.Description() {
			t.Errorf("%s: DescriptionIn(A1B) = %q, want the Part 1 description", c.Name(), got)
		}
	}
	for _, c := range AllChecks() {
		_, numbered := part2Clauses[c.Name()]
		if want := numbered || c.Clause() == ObjectModelClause; c.AppliesTo(A2B) != want {
			t.Errorf("%s: AppliesTo(A2B) = %v, want %v", c.Name(), c.AppliesTo(A2B), want)
		}
	}
}

// TestPart2SharingStaysWithinAPart1Clause verifies that checks share a Part 2
// clause and subclause only when they enforce the same Part 1 clause, so a
// copied entry on an unrelated check is caught.
func TestPart2SharingStaysWithinAPart1Clause(t *testing.T) {
	owner := map[clauseRef]Check{}
	byName := map[string]Check{}
	for _, c := range AllChecks() {
		byName[c.Name()] = c
	}
	for name, ref := range part2Clauses {
		c := byName[name]
		key := clauseRef{clause: ref.clause, subclause: ref.subclause}
		if prev, ok := owner[key]; ok && prev.Clause() != c.Clause() {
			t.Errorf("%s (Part 1 %s) and %s (Part 1 %s) share the Part 2 rule %s/%d",
				prev.Name(), prev.Clause(), c.Name(), c.Clause(), ref.clause, ref.subclause)
		}
		owner[key] = c
	}
}

// TestSharedPart2Clause verifies that the checks sharing one veraPDF rule are
// all found under its clause, and that CheckByClauseIn picks the first of
// them in catalogue order.
func TestSharedPart2Clause(t *testing.T) {
	got := ChecksForClauseIn(A2B, "6.1.2")
	want := []Check{
		Checks.Structure.FileHeaderSignature,
		Checks.Structure.FileHeaderComment,
		Checks.Structure.FileHeaderCommentLength,
		Checks.Structure.FileHeaderCommentBytes,
	}
	if !slices.Equal(got, want) {
		t.Errorf("ChecksForClauseIn(A2B, 6.1.2) = %v, want %v", got, want)
	}
	if c, ok := CheckByClauseIn(A2B, "6.1.2", 2); !ok || c != Checks.Structure.FileHeaderComment {
		t.Errorf("CheckByClauseIn(A2B, 6.1.2, 2) = %s, %v", c.Name(), ok)
	}
}

// TestFullProfilesEnableTheirPart verifies that a full profile enables exactly
// the checks that apply to its level: the Part 1 profiles keep the whole Part 1
// catalogue, Level A requirements included, and PDFA2B enables the checks for
// which AppliesTo(A2B) is true.
func TestFullProfilesEnableTheirPart(t *testing.T) {
	for _, tc := range []struct {
		p    *Profile
		want func(Check) bool
	}{
		{Legacy1B, func(c Check) bool { return c.Clause() != "" }},
		{NewFullProfile(A1B), func(c Check) bool { return c.Clause() != "" }},
		{PDFA2B, func(c Check) bool { return c.AppliesTo(A2B) }},
		{NewFullProfile(ObjectModel), func(c Check) bool { return c.Clause() == ObjectModelClause }},
		{NewFullProfile(Undefined), func(Check) bool { return false }},
	} {
		for _, c := range AllChecks() {
			if got := tc.p.Has(c); got != tc.want(c) {
				t.Errorf("%s: Has(%s) = %v", tc.p, c.Name(), got)
			}
		}
	}
	if !PDFA2B.SkipUnreachableXObjects || !PDFA2B.SkipUnusedSimpleFonts {
		t.Errorf("PDFA2B lost veraPDF's leniencies: %+v", PDFA2B)
	}
}

// TestNewPart2CheckRequiresAPart2Clause verifies that newPart2Check panics
// when part2Clauses does not number the check, since such a check would apply
// to no level.
func TestNewPart2CheckRequiresAPart2Clause(t *testing.T) {
	savedLen := len(allChecksCatalog)
	defer func() {
		if r := recover(); r == nil {
			t.Error("newPart2Check did not panic on a check without a Part 2 clause")
		}
		if len(allChecksCatalog) != savedLen {
			t.Errorf("catalog grew from %d to %d on a rejected registration", savedLen, len(allChecksCatalog))
		}
	}()
	newPart2Check(9999, "Unnumbered", "has no Part 2 clause")
}
