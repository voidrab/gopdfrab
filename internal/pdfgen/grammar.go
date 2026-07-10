package pdfgen

import (
	"fmt"
	"math/rand"
	"strings"
)

// generateGrammar builds a random but syntactically-plausible PDF from scratch
// (rather than by corrupting a fixed seed): a valid Catalog/Pages skeleton plus
// a handful of objects whose bodies are randomly-generated PDF values -- nested
// arrays/dicts, names, numbers (including extreme values), strings, and
// references (some dangling). This reaches object-graph shapes that corrupting
// one of the fixed seeds never produces.
func generateGrammar(rng *rand.Rand) []byte {
	n := 3 + rng.Intn(12)
	b := NewBuilder("%PDF-1.7\n" + binaryMarker)
	b.Obj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	b.Obj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	b.Obj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Resources << >> >>")
	for i := 4; i <= n; i++ {
		if rng.Intn(3) == 0 {
			raw := randBytes(rng, rng.Intn(64))
			b.StreamObj(i, randDictHead(rng, n, 0), raw)
		} else {
			b.Obj(i, randValueString(rng, n, 0))
		}
	}
	return b.FinishClassic(fmt.Sprintf("<< /Size %d /Root 1 0 R >>", n+1))
}

const grammarMaxDepth = 4

// randValueString returns a random PDF object serialization.
func randValueString(rng *rand.Rand, maxObj, depth int) string {
	if depth >= grammarMaxDepth {
		return randScalar(rng, maxObj)
	}
	switch rng.Intn(7) {
	case 0:
		return randArray(rng, maxObj, depth)
	case 1:
		return randDict(rng, maxObj, depth)
	default:
		return randScalar(rng, maxObj)
	}
}

func randScalar(rng *rand.Rand, maxObj int) string {
	switch rng.Intn(6) {
	case 0:
		return randName(rng)
	case 1:
		return randNumber(rng)
	case 2:
		return "(" + randLiteralBody(rng) + ")"
	case 3:
		return randRef(rng, maxObj)
	case 4:
		return []string{"true", "false", "null"}[rng.Intn(3)]
	default:
		return randNumber(rng)
	}
}

func randArray(rng *rand.Rand, maxObj, depth int) string {
	var sb strings.Builder
	sb.WriteString("[ ")
	for i, k := 0, rng.Intn(6); i < k; i++ {
		sb.WriteString(randValueString(rng, maxObj, depth+1))
		sb.WriteByte(' ')
	}
	sb.WriteByte(']')
	return sb.String()
}

func randDict(rng *rand.Rand, maxObj, depth int) string {
	return randDictHead(rng, maxObj, depth) + " >>"
}

// randDictHead returns "<< /k v ..." WITHOUT the closing ">>", so StreamObj can
// append its own /Length and delimiter.
func randDictHead(rng *rand.Rand, maxObj, depth int) string {
	var sb strings.Builder
	sb.WriteString("<<")
	for i, k := 0, rng.Intn(5); i < k; i++ {
		sb.WriteByte(' ')
		sb.WriteString(randName(rng))
		sb.WriteByte(' ')
		sb.WriteString(randValueString(rng, maxObj, depth+1))
	}
	return sb.String()
}

func randName(rng *rand.Rand) string {
	const alpha = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOP0123456789"
	n := 1 + rng.Intn(8)
	var sb strings.Builder
	sb.WriteByte('/')
	for i := 0; i < n; i++ {
		sb.WriteByte(alpha[rng.Intn(len(alpha))])
	}
	return sb.String()
}

func randNumber(rng *rand.Rand) string {
	switch rng.Intn(6) {
	case 0:
		return "0"
	case 1:
		return "-1"
	case 2:
		return fmt.Sprintf("%d", rng.Int63()) // large
	case 3:
		return fmt.Sprintf("-%d", rng.Int63())
	case 4:
		return fmt.Sprintf("%d.%d", rng.Intn(1000), rng.Intn(1000))
	default:
		return fmt.Sprintf("%d", rng.Intn(100))
	}
}

func randRef(rng *rand.Rand, maxObj int) string {
	// Half the time reference a real object, half the time dangle.
	if rng.Intn(2) == 0 {
		return fmt.Sprintf("%d 0 R", 1+rng.Intn(maxObj))
	}
	return fmt.Sprintf("%d 0 R", maxObj+1+rng.Intn(100000))
}

func randLiteralBody(rng *rand.Rand) string {
	n := rng.Intn(16)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		c := byte(0x20 + rng.Intn(0x5e))
		if c == '(' || c == ')' || c == '\\' {
			sb.WriteByte('\\')
		}
		sb.WriteByte(c)
	}
	return sb.String()
}

func randBytes(rng *rand.Rand, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(rng.Intn(256))
	}
	return out
}
