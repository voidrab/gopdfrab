package pdfrab

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// postScriptFunction implements FunctionType 4: a small stack machine
// interpreting the arithmetic/comparison/stack PostScript subset PDF allows.
type postScriptFunction struct {
	domain   []float64
	rangeArr []float64
	program  []psItem
}

// psItem is one parsed element of a Type 4 program: either a number
// literal, an operator keyword, or a `{...}` procedure (only meaningful as
// an operand to if/ifelse).
type psItem struct {
	isNumber bool
	isProc   bool
	number   float64
	op       string
	proc     []psItem
}

func newPostScriptFunction(d PDFDict, domain []float64) (*postScriptFunction, error) {
	data, err := decodeStream(d)
	if err != nil {
		return nil, fmt.Errorf("pdffunc: PostScript calculator stream: %w", err)
	}

	tokens := tokenizePostScriptCalculator(data)
	pos := 0
	if pos >= len(tokens) || tokens[pos] != "{" {
		return nil, fmt.Errorf("pdffunc: PostScript calculator function must start with '{'")
	}
	pos++
	program, pos, err := parsePostScriptProgram(tokens, pos)
	if err != nil {
		return nil, fmt.Errorf("pdffunc: %w", err)
	}

	var rangeArr []float64
	if v, ok := d.Entries["Range"]; ok {
		if rangeArr, err = floatArray(v); err != nil {
			return nil, fmt.Errorf("pdffunc: Range: %w", err)
		}
	}

	return &postScriptFunction{domain: domain, rangeArr: rangeArr, program: program}, nil
}

// tokenizePostScriptCalculator splits a Type 4 program into tokens, treating
// '{' and '}' as standalone tokens regardless of surrounding whitespace.
func tokenizePostScriptCalculator(data []byte) []string {
	s := string(data)
	s = strings.ReplaceAll(s, "{", " { ")
	s = strings.ReplaceAll(s, "}", " } ")
	return strings.Fields(s)
}

// parsePostScriptProgram parses tokens starting at pos until (and
// consuming) the matching '}', returning the parsed items and the position
// just past it.
func parsePostScriptProgram(tokens []string, pos int) ([]psItem, int, error) {
	var items []psItem
	for pos < len(tokens) {
		tok := tokens[pos]
		switch tok {
		case "}":
			return items, pos + 1, nil
		case "{":
			proc, next, err := parsePostScriptProgram(tokens, pos+1)
			if err != nil {
				return nil, 0, err
			}
			items = append(items, psItem{isProc: true, proc: proc})
			pos = next
		default:
			if n, err := strconv.ParseFloat(tok, 64); err == nil {
				items = append(items, psItem{isNumber: true, number: n})
			} else {
				items = append(items, psItem{op: tok})
			}
			pos++
		}
	}
	return nil, 0, fmt.Errorf("unterminated procedure (missing '}')")
}

func (f *postScriptFunction) Eval(in []float64) []float64 {
	in = clampDomain(in, f.domain)
	stack := make([]psValue, 0, len(in)+8)
	for _, x := range in {
		stack = append(stack, psValue{number: x})
	}

	stack, _ = execPostScript(f.program, stack)

	n := len(f.rangeArr) / 2
	if n == 0 || n > len(stack) {
		n = len(stack)
	}
	out := make([]float64, n)
	start := len(stack) - n
	for i := 0; i < n; i++ {
		out[i] = stack[start+i].number
	}
	if f.rangeArr != nil {
		out = clampDomain(out, f.rangeArr)
	}
	return out
}

// psValue is a Type 4 stack slot: a number, or (only as an if/ifelse
// operand) a procedure body.
type psValue struct {
	number float64
	proc   []psItem
	isProc bool
}

// execPostScript runs a parsed program against stack, returning the
// resulting stack.
func execPostScript(program []psItem, stack []psValue) ([]psValue, error) {
	pop := func() (psValue, error) {
		if len(stack) == 0 {
			return psValue{}, fmt.Errorf("stack underflow")
		}
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		return v, nil
	}
	popNum := func() (float64, error) {
		v, err := pop()
		if err != nil {
			return 0, err
		}
		return v.number, nil
	}
	push := func(n float64) { stack = append(stack, psValue{number: n}) }
	pushBool := func(b bool) {
		if b {
			push(1)
		} else {
			push(0)
		}
	}

	for _, item := range program {
		switch {
		case item.isNumber:
			push(item.number)
		case item.isProc:
			stack = append(stack, psValue{isProc: true, proc: item.proc})
		default:
			switch item.op {
			case "add":
				b, _ := popNum()
				a, _ := popNum()
				push(a + b)
			case "sub":
				b, _ := popNum()
				a, _ := popNum()
				push(a - b)
			case "mul":
				b, _ := popNum()
				a, _ := popNum()
				push(a * b)
			case "div":
				b, _ := popNum()
				a, _ := popNum()
				if b == 0 {
					push(0)
				} else {
					push(a / b)
				}
			case "neg":
				a, _ := popNum()
				push(-a)
			case "abs":
				a, _ := popNum()
				push(math.Abs(a))
			case "sqrt":
				a, _ := popNum()
				push(math.Sqrt(a))
			case "exp":
				b, _ := popNum()
				a, _ := popNum()
				push(math.Pow(a, b))
			case "ln":
				a, _ := popNum()
				push(math.Log(a))
			case "log":
				a, _ := popNum()
				push(math.Log10(a))
			case "cvi":
				a, _ := popNum()
				push(math.Trunc(a))
			case "cvr":
				// no-op: this stack has no integer/real distinction.
			case "eq":
				b, _ := popNum()
				a, _ := popNum()
				pushBool(a == b)
			case "ne":
				b, _ := popNum()
				a, _ := popNum()
				pushBool(a != b)
			case "gt":
				b, _ := popNum()
				a, _ := popNum()
				pushBool(a > b)
			case "ge":
				b, _ := popNum()
				a, _ := popNum()
				pushBool(a >= b)
			case "lt":
				b, _ := popNum()
				a, _ := popNum()
				pushBool(a < b)
			case "le":
				b, _ := popNum()
				a, _ := popNum()
				pushBool(a <= b)
			case "and":
				b, _ := popNum()
				a, _ := popNum()
				pushBool(a != 0 && b != 0)
			case "or":
				b, _ := popNum()
				a, _ := popNum()
				pushBool(a != 0 || b != 0)
			case "not":
				a, _ := popNum()
				pushBool(a == 0)
			case "dup":
				v, err := pop()
				if err == nil {
					stack = append(stack, v, v)
				}
			case "pop":
				pop()
			case "exch":
				b, _ := pop()
				a, _ := pop()
				stack = append(stack, b, a)
			case "index":
				n, _ := popNum()
				i := len(stack) - 1 - int(n)
				if i >= 0 && i < len(stack) {
					stack = append(stack, stack[i])
				}
			case "copy":
				n, _ := popNum()
				k := int(n)
				if k > 0 && k <= len(stack) {
					stack = append(stack, stack[len(stack)-k:]...)
				}
			case "roll":
				j, _ := popNum()
				n, _ := popNum()
				rollStack(stack, int(n), int(j))
			case "if":
				proc, _ := pop()
				cond, _ := popNum()
				if cond != 0 && proc.isProc {
					var err error
					stack, err = execPostScript(proc.proc, stack)
					if err != nil {
						return stack, err
					}
				}
			case "ifelse":
				proc2, _ := pop()
				proc1, _ := pop()
				cond, _ := popNum()
				chosen := proc2
				if cond != 0 {
					chosen = proc1
				}
				if chosen.isProc {
					var err error
					stack, err = execPostScript(chosen.proc, stack)
					if err != nil {
						return stack, err
					}
				}
			default:
				return stack, fmt.Errorf("unsupported PostScript calculator operator %q", item.op)
			}
		}
	}
	return stack, nil
}

// rollStack performs PostScript's `n j roll`: rotates the top n elements of
// stack by j positions (positive j rolls toward the top), in place.
func rollStack(stack []psValue, n, j int) {
	if n <= 0 || n > len(stack) {
		return
	}
	top := stack[len(stack)-n:]
	j = ((j % n) + n) % n
	rotated := make([]psValue, n)
	for i := 0; i < n; i++ {
		rotated[(i+j)%n] = top[i]
	}
	copy(top, rotated)
}
