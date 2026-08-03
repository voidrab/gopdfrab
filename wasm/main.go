//go:build js && wasm

// Command wasm is a syscall/js thin wrapper around gopdfrab that exposes
// VerifyBytes and ConvertBytes as awaitable JavaScript functions registered on
// the global object.
//
// Build with:
//
//	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o gopdfrab.wasm ./wasm
package main

import (
	"fmt"
	"reflect"
	"syscall/js"

	"github.com/voidrab/gopdfrab"
)

// checkGroups maps every registered check to the name of the gopdfrab.Checks
// field it lives under ("Font", "Colour", …), so the UI can group issues the
// same way the library's registry does. The library exposes no group accessor
// and clause prefixes do not identify a group reliably, so the mapping is
// recovered from the registry's own shape.
var checkGroups = buildCheckGroups()

func buildCheckGroups() map[gopdfrab.Check]string {
	groups := make(map[gopdfrab.Check]string)
	checkType := reflect.TypeOf(gopdfrab.Check{})

	registry := reflect.ValueOf(gopdfrab.Checks)
	for i := range registry.NumField() {
		group := registry.Type().Field(i).Name
		fields := registry.Field(i)
		for j := range fields.NumField() {
			if f := fields.Field(j); f.Type() == checkType {
				groups[f.Interface().(gopdfrab.Check)] = group
			}
		}
	}
	return groups
}

func main() {
	js.Global().Set("gopdfrabVerify", js.FuncOf(jsVerify))
	js.Global().Set("gopdfrabConvert", js.FuncOf(jsConvert))

	// Signal readiness to the worker host.
	js.Global().Call("postMessage", map[string]any{"type": "ready"})

	// Block forever to keep the WASM instance alive.
	select {}
}

// jsVerify implements:
//
//	gopdfrabVerify(bytes: Uint8Array) → Promise<{
//	  valid: boolean,
//	  summary: string,
//	  profile: string,
//	  issueCount: number,
//	  doc: DocInfo,
//	  issues: Issue[]
//	}>
//
// See jsIssue for the Issue shape and jsDocInfo for DocInfo.
func jsVerify(_ js.Value, args []js.Value) any {
	return newPromise(func() (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("gopdfrabVerify: expected 1 argument")
		}
		data := copyBytes(args[0])
		result, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA1B)
		if err != nil {
			return nil, fmt.Errorf("verify: %w", err)
		}

		return map[string]any{
			"valid":      result.Valid,
			"summary":    result.Summary(),
			"profile":    string(result.Type),
			"issueCount": result.Count(),
			"doc":        jsDocInfo(data),
			"issues":     jsIssues(result.Issues),
		}, nil
	})
}

// jsConvert implements:
//
//	gopdfrabConvert(bytes: Uint8Array) → Promise<{
//	  valid: boolean,
//	  iterations: number,
//	  output: Uint8Array,
//	  doc: DocInfo,
//	  before: { valid: boolean, issueCount: number },
//	  resolved: Issue[],
//	  residual: Issue[],
//	  rasterizedPages: number[],
//	  rasterDrops: Array<{ page: number, features: string[] }>,
//	  lostObjects: Issue[]
//	}>
//
// The input is verified before conversion so the caller can be told which of
// its original defects were resolved -- ConvertResult only reports what is
// left over.
func jsConvert(_ js.Value, args []js.Value) any {
	return newPromise(func() (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("gopdfrabConvert: expected 1 argument")
		}
		data := copyBytes(args[0])

		// One parse serves both the document facts and the pre-conversion
		// verify. A file too damaged to open still converts: Convert does its
		// own recovery, so a failure here only costs the "resolved" list.
		var before gopdfrab.Result
		beforeOK := false
		if doc, err := gopdfrab.OpenBytes(data); err == nil {
			before, err = doc.Verify(gopdfrab.PDFA1B)
			beforeOK = err == nil
			doc.Close()
		}

		cr, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA1B)
		if err != nil {
			return nil, fmt.Errorf("convert: %w", err)
		}
		defer cr.Close()

		residual := cr.Residual()

		// Resolved = everything the input violated that the output no longer
		// does, keyed by check so repeat occurrences collapse to one entry.
		var resolved []gopdfrab.PDFError
		if beforeOK {
			left := make(map[gopdfrab.Check]bool, len(residual))
			for _, iss := range residual {
				left[iss.Check()] = true
			}
			seen := make(map[gopdfrab.Check]bool)
			for _, iss := range before.Issues {
				c := iss.Check()
				if !left[c] && !seen[c] {
					seen[c] = true
					resolved = append(resolved, iss)
				}
			}
		}

		rasterDrops := make([]any, 0, len(cr.RasterDrops))
		for _, d := range cr.RasterDrops {
			rasterDrops = append(rasterDrops, map[string]any{
				"page":     d.Page,
				"features": jsStrings(d.Features),
			})
		}

		// Copy output bytes into a JS Uint8Array. On wasm the output always
		// stays in memory (no filesystem to spill to), so Output never errors.
		outBytes, err := cr.Output()
		if err != nil {
			return nil, err
		}
		jsOut := js.Global().Get("Uint8Array").New(len(outBytes))
		js.CopyBytesToJS(jsOut, outBytes)

		result := map[string]any{
			"valid":           cr.Result.Valid,
			"iterations":      cr.Iterations,
			"output":          jsOut,
			"doc":             jsDocInfo(data),
			"resolved":        jsIssues(resolved),
			"residual":        jsIssues(residual),
			"rasterizedPages": jsInts(cr.RasterizedPages),
			"rasterDrops":     rasterDrops,
			"lostObjects":     jsIssues(cr.LostObjects),
		}
		if beforeOK {
			result["before"] = map[string]any{
				"valid":      before.Valid,
				"issueCount": before.Count(),
			}
		}
		return result, nil
	})
}

// jsIssue converts one PDFError to:
//
//	{ clause, subclause, name, description, group, page, documentLevel, messages }
func jsIssue(iss gopdfrab.PDFError) any {
	c := iss.Check()
	return map[string]any{
		"clause":        c.Clause(),
		"subclause":     c.Subclause(),
		"name":          c.Name(),
		"description":   c.Description(),
		"group":         checkGroups[c],
		"page":          iss.Page(),
		"documentLevel": iss.IsDocumentLevel(),
		"messages":      jsStrings(iss.Messages()),
	}
}

func jsIssues(issues []gopdfrab.PDFError) []any {
	out := make([]any, 0, len(issues))
	for _, iss := range issues {
		out = append(out, jsIssue(iss))
	}
	return out
}

// jsDocInfo reports what the file says about itself:
//
//	{ pageCount?, version?, claimedPart?, claimedLevel?, title?, author? }
//
// Every field is best-effort. A document that cannot be opened, or an accessor
// that fails on a damaged file, omits its field rather than failing the call --
// verification and conversion must still report on a file this cannot describe.
func jsDocInfo(data []byte) any {
	info := map[string]any{}

	doc, err := gopdfrab.OpenBytes(data)
	if err != nil {
		return info
	}
	defer doc.Close()

	if n, err := doc.PageCount(); err == nil {
		info["pageCount"] = n
	}
	if v, err := doc.Version(); err == nil && v != "" {
		info["version"] = v
	}
	if part, level, err := doc.ClaimedConformance(); err == nil && part != "" {
		info["claimedPart"] = part
		info["claimedLevel"] = level
	}
	if md, err := doc.Metadata(); err == nil {
		if t := md["Title"]; t != "" {
			info["title"] = t
		}
		if a := md["Author"]; a != "" {
			info["author"] = a
		}
	}
	return info
}

func jsStrings(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func jsInts(ns []int) []any {
	out := make([]any, len(ns))
	for i, n := range ns {
		out[i] = n
	}
	return out
}

// copyBytes copies a JS Uint8Array into a Go byte slice.
func copyBytes(v js.Value) []byte {
	buf := make([]byte, v.Length())
	js.CopyBytesToGo(buf, v)
	return buf
}

// newPromise wraps a synchronous Go function in a JS Promise, running it in a
// goroutine so callers can await without blocking the event loop. Panics inside
// fn are recovered and turned into rejections.
func newPromise(fn func() (any, error)) js.Value {
	promise, resolve, reject := makePromise()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				reject.Invoke(fmt.Sprintf("panic: %v", r))
			}
		}()
		result, err := fn()
		if err != nil {
			reject.Invoke(err.Error())
			return
		}
		resolve.Invoke(js.ValueOf(result))
	}()
	return promise
}

// makePromise returns a JS Promise together with its resolve/reject callbacks.
func makePromise() (promise, resolve, reject js.Value) {
	var resolveFn, rejectFn js.Value
	promise = js.Global().Get("Promise").New(js.FuncOf(func(_ js.Value, args []js.Value) any {
		resolveFn = args[0]
		rejectFn = args[1]
		return nil
	}))
	return promise, resolveFn, rejectFn
}
