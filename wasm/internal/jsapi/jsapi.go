//go:build js && wasm

// Package jsapi is the syscall/js bridge both browser builds share: the
// verify entry point, the JavaScript shapes of issues and document facts, and
// the Promise plumbing. It is written against gopdfrab/verify alone, so the
// verify-only module links no conversion code; the full module reuses it
// because gopdfrab's types are the same types.
package jsapi

import (
	"fmt"
	"reflect"
	"syscall/js"

	"github.com/voidrab/gopdfrab/verify"
)

// checkGroups maps every registered check to the name of the verify.Checks
// field it lives under ("Font", "Colour", …), so the UI can group issues the
// same way the library's registry does. The library exposes no group accessor
// and clause prefixes do not identify a group reliably, so the mapping is
// recovered from the registry's own shape.
var checkGroups = buildCheckGroups()

func buildCheckGroups() map[verify.Check]string {
	groups := make(map[verify.Check]string)
	checkType := reflect.TypeOf(verify.Check{})

	registry := reflect.ValueOf(verify.Checks)
	for i := range registry.NumField() {
		group := registry.Type().Field(i).Name
		fields := registry.Field(i)
		for j := range fields.NumField() {
			if f := fields.Field(j); f.Type() == checkType {
				groups[f.Interface().(verify.Check)] = group
			}
		}
	}
	return groups
}

// Ready tells the worker host that this module's globals are registered.
// module names which build is speaking ("verify" or "convert"), so a host
// running both can tell their signals apart.
func Ready(module string) {
	js.Global().Call("postMessage", map[string]any{"type": "ready", "module": module})
}

// Verify implements:
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
// See Issue for the Issue shape and DocInfo for DocInfo.
func Verify(_ js.Value, args []js.Value) any {
	return NewPromise(func() (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("gopdfrabVerify: expected 1 argument")
		}
		data := CopyBytes(args[0])
		result, err := verify.VerifyBytes(data, verify.PDFA1B)
		if err != nil {
			return nil, fmt.Errorf("verify: %w", err)
		}

		return map[string]any{
			"valid":      result.Valid,
			"summary":    result.Summary(),
			"profile":    string(result.Type),
			"issueCount": result.Count(),
			"doc":        DocInfo(data),
			"issues":     Issues(result.Issues),
		}, nil
	})
}

// Issue converts one PDFError to:
//
//	{ clause, subclause, name, description, group, page, documentLevel, messages }
func Issue(iss verify.PDFError) any {
	c := iss.Check()
	return map[string]any{
		"clause":        c.Clause(),
		"subclause":     c.Subclause(),
		"name":          c.Name(),
		"description":   c.Description(),
		"group":         checkGroups[c],
		"page":          iss.Page(),
		"documentLevel": iss.IsDocumentLevel(),
		"messages":      Strings(iss.Messages()),
	}
}

// Issues converts each of issues with Issue.
func Issues(issues []verify.PDFError) []any {
	out := make([]any, 0, len(issues))
	for _, iss := range issues {
		out = append(out, Issue(iss))
	}
	return out
}

// DocInfo reports what the file says about itself:
//
//	{ pageCount?, version?, claimedPart?, claimedLevel?, title?, author? }
//
// Every field is best-effort. A document that cannot be opened, or an accessor
// that fails on a damaged file, omits its field rather than failing the call --
// verification and conversion must still report on a file this cannot describe.
func DocInfo(data []byte) any {
	info := map[string]any{}

	doc, err := verify.OpenBytes(data)
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

// Strings converts ss to a JS-marshalable slice.
func Strings(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// Ints converts ns to a JS-marshalable slice.
func Ints(ns []int) []any {
	out := make([]any, len(ns))
	for i, n := range ns {
		out[i] = n
	}
	return out
}

// CopyBytes copies a JS Uint8Array into a Go byte slice.
func CopyBytes(v js.Value) []byte {
	buf := make([]byte, v.Length())
	js.CopyBytesToGo(buf, v)
	return buf
}

// NewPromise wraps a synchronous Go function in a JS Promise, running it in a
// goroutine so callers can await without blocking the event loop. Panics inside
// fn are recovered and turned into rejections.
func NewPromise(fn func() (any, error)) js.Value {
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
