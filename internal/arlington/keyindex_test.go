package arlington

import "testing"

// TestKeyIndexMatchesLinearScan cross-checks the init-built keyByName index
// against the front-to-back scan semantics it replaced, for every type in
// the generated model: the named row (first wins on duplicates), the
// wildcard fallback, and plain membership.
func TestKeyIndexMatchesLinearScan(t *testing.T) {
	if len(Types) == 0 {
		t.Fatal("generated Types table is empty")
	}
	for name, ot := range Types {
		for i := range ot.Keys {
			key := ot.Keys[i].Name
			var want *KeyDef
			for j := range ot.Keys {
				if ot.Keys[j].Name == key {
					want = &ot.Keys[j]
					break
				}
			}
			if got := ot.KeyDefByName(key); got != want {
				t.Fatalf("%s: KeyDefByName(%q) = %p, want first matching row %p", name, key, got, want)
			}
			if !ot.HasNamedKey(key) {
				t.Fatalf("%s: HasNamedKey(%q) = false for an existing row", name, key)
			}
		}
		if ot.HasNamedKey("NoSuchKeyEver") {
			t.Fatalf("%s: HasNamedKey of a bogus key = true", name)
		}
		if got := ot.KeyDefByName("NoSuchKeyEver"); got != ot.Wildcard {
			t.Fatalf("%s: KeyDefByName of a bogus key = %p, want the wildcard %p", name, got, ot.Wildcard)
		}
	}
}

// TestKeyLookupFallbackScan exercises the linear-scan fallback used by
// hand-built ObjectType values, which have no keyByName index.
func TestKeyLookupFallbackScan(t *testing.T) {
	wc := &KeyDef{Name: "*"}
	ot := ObjectType{
		Name:     "Synthetic",
		Keys:     []KeyDef{{Name: "A"}, {Name: "B"}},
		Wildcard: wc,
	}

	if !ot.HasNamedKey("B") {
		t.Error("HasNamedKey(B) = false, want true")
	}
	if ot.HasNamedKey("C") {
		t.Error("HasNamedKey(C) = true, want false")
	}
	if got := ot.KeyDefByName("A"); got != &ot.Keys[0] {
		t.Errorf("KeyDefByName(A) = %p, want &Keys[0]", got)
	}
	if got := ot.KeyDefByName("C"); got != wc {
		t.Errorf("KeyDefByName(C) = %p, want the wildcard", got)
	}

	noWildcard := ObjectType{Keys: []KeyDef{{Name: "A"}}}
	if got := noWildcard.KeyDefByName("Z"); got != nil {
		t.Errorf("KeyDefByName(Z) with no wildcard = %p, want nil", got)
	}
}
