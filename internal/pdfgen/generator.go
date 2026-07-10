package pdfgen

import "math/rand"

// Generator produces deterministic, deliberately-broken PDF documents by taking
// a structurally-valid seed and applying a random chain of corruptors. It holds
// no mutable state, so it is safe for concurrent use.
type Generator struct {
	seeds      [][]byte
	corruptors []Corruptor
}

// New returns a Generator over the default seed corpus and corruptor table.
func New() *Generator {
	return &Generator{seeds: Seeds(), corruptors: Corruptors()}
}

// Generate returns one broken PDF derived deterministically from seed. The same
// seed always yields byte-identical output, so any crash a fuzzer or stress
// test finds is trivially reproducible from its seed alone.
func (g *Generator) Generate(seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))

	// Occasionally emit pure garbage instead of a corrupted valid document, to
	// exercise the "not a PDF at all" rejection paths.
	if rng.Intn(8) == 0 {
		return pureGarbage(rng)
	}

	// Sometimes build a fresh random object graph from the grammar and (usually)
	// corrupt it, reaching shapes corrupting a fixed seed never produces.
	if rng.Intn(6) == 0 {
		out := generateGrammar(rng)
		if rng.Intn(2) == 0 {
			return out
		}
		c := g.corruptors[rng.Intn(len(g.corruptors))]
		return c.Apply(out, rng)
	}

	base := g.seeds[rng.Intn(len(g.seeds))]
	out := cp(base)
	n := 1 + rng.Intn(3) // apply 1..3 corruptors in sequence
	for i := 0; i < n; i++ {
		c := g.corruptors[rng.Intn(len(g.corruptors))]
		out = c.Apply(out, rng)
	}
	return out
}

// GenerateN returns n broken PDFs derived from consecutive seeds starting at
// seed.
func (g *Generator) GenerateN(seed int64, n int) [][]byte {
	out := make([][]byte, n)
	for i := 0; i < n; i++ {
		out[i] = g.Generate(seed + int64(i))
	}
	return out
}

// pureGarbage produces random bytes, sometimes wearing a plausible PDF header,
// to hit the earliest header/structure-detection code.
func pureGarbage(rng *rand.Rand) []byte {
	n := rng.Intn(512)
	out := make([]byte, 0, n+16)
	if rng.Intn(2) == 0 {
		out = append(out, "%PDF-1."...)
		out = append(out, byte('0'+rng.Intn(10)))
		out = append(out, '\n')
	}
	for i := 0; i < n; i++ {
		out = append(out, byte(rng.Intn(256)))
	}
	return out
}

var defaultGenerator = New()

// Generate is a package-level convenience over a shared default Generator.
func Generate(seed int64) []byte { return defaultGenerator.Generate(seed) }

// GenerateN is a package-level convenience over a shared default Generator.
func GenerateN(seed int64, n int) [][]byte { return defaultGenerator.GenerateN(seed, n) }

// GenerateGrammar builds a fresh random object graph from the PDF grammar
// (rather than by corrupting a fixed seed), deterministically from seed. It is
// the generative counterpart to Generate.
func GenerateGrammar(seed int64) []byte {
	return generateGrammar(rand.New(rand.NewSource(seed)))
}
