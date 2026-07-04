package pdf

import (
	"strconv"
	"testing"
)

func TestCatalog_AllChecksUnique(t *testing.T) {
	checks := AllChecks()
	if len(checks) == 0 {
		t.Fatal("AllChecks returned an empty catalog")
	}
	seenIDs := map[int]bool{}
	seenPairs := map[string]bool{}
	for _, c := range checks {
		if c.ID() == 0 {
			t.Errorf("check %q has zero (unregistered) ID", c.Name())
		}
		if seenIDs[c.ID()] {
			t.Errorf("duplicate ID %d for check %q", c.ID(), c.Name())
		}
		seenIDs[c.ID()] = true

		pair := c.Clause() + "/" + strconv.Itoa(c.Subclause())
		if seenPairs[pair] {
			t.Errorf("duplicate (clause, subclause) pair %s for check %q", pair, c.Name())
		}
		seenPairs[pair] = true

		if c.Name() == "" {
			t.Errorf("check with ID %d has empty name", c.ID())
		}
		if c.Description() == "" {
			t.Errorf("check %q has empty description", c.Name())
		}
		if c.Clause() == "" {
			t.Errorf("check %q has empty clause", c.Name())
		}
	}
}

func TestCatalog_KnownChecks(t *testing.T) {
	for _, tc := range []struct {
		check  Check
		clause string
		sub    int
		name   string
	}{
		{Checks.Transparency.ImageWithSoftMask, "6.4", 6, "ImageWithSoftMask"},
		{Checks.Structure.FileHeaderSignature, "6.1.2", 1, "FileHeaderSignature"},
		{Checks.Structure.ObjectFraming, "6.1.8", 1, "ObjectFraming"},
		{Checks.Metadata.PDFAIdentifierMissing, "6.7.11", 1, "PDFAIdentifierMissing"},
		{Checks.Font.AdvanceWidthMismatch, "6.3.6", 1, "AdvanceWidthMismatch"},
		{Checks.Action.AdditionalActions, "6.6.2", 1, "AdditionalActions"},
		{Checks.Form.XFA, "6.9", 2, "XFA"},
		{Checks.Annotation.DisallowedSubtype, "6.5.2", 1, "DisallowedSubtype"},
	} {
		if tc.check.Clause() != tc.clause {
			t.Errorf("%s: Clause() = %q, want %q", tc.name, tc.check.Clause(), tc.clause)
		}
		if tc.check.Subclause() != tc.sub {
			t.Errorf("%s: Subclause() = %d, want %d", tc.name, tc.check.Subclause(), tc.sub)
		}
		if tc.check.Name() != tc.name {
			t.Errorf("Name() = %q, want %q", tc.check.Name(), tc.name)
		}
		if tc.check.ID() == 0 {
			t.Errorf("%s has zero ID (not registered)", tc.name)
		}
	}
}

func TestCheckByName(t *testing.T) {
	all := AllChecks()
	if len(all) == 0 {
		t.Fatal("no checks registered")
	}
	name := all[0].Name()
	if c, ok := CheckByName(name); !ok || c.Name() != name {
		t.Errorf("CheckByName(%q) = %v, %v", name, c, ok)
	}
	if _, ok := CheckByName("no such check name"); ok {
		t.Error("CheckByName(unknown) should be false")
	}
}
