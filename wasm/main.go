//go:build js && wasm

// Command wasm is the full browser build: a syscall/js thin wrapper around
// gopdfrab that exposes VerifyBytes and ConvertBytes as awaitable JavaScript
// functions registered on the global object. A host that only verifies should
// load the smaller verify-only module in ./wasm/verify instead, and this one
// only when it converts.
//
// Build with:
//
//	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o gopdfrab.wasm ./wasm
package main

import (
	"fmt"
	"syscall/js"

	"github.com/voidrab/gopdfrab"
	"github.com/voidrab/gopdfrab/wasm/internal/jsapi"
)

func main() {
	js.Global().Set("gopdfrabVerify", js.FuncOf(jsapi.Verify))
	js.Global().Set("gopdfrabConvert", js.FuncOf(jsConvert))

	// Signal readiness to the worker host.
	jsapi.Ready("convert")

	// Block forever to keep the WASM instance alive.
	select {}
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
// left over. jsapi.Issue and jsapi.DocInfo document the Issue and DocInfo
// shapes.
func jsConvert(_ js.Value, args []js.Value) any {
	return jsapi.NewPromise(func() (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("gopdfrabConvert: expected 1 argument")
		}
		data := jsapi.CopyBytes(args[0])

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
				"features": jsapi.Strings(d.Features),
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
			"doc":             jsapi.DocInfo(data),
			"resolved":        jsapi.Issues(resolved),
			"residual":        jsapi.Issues(residual),
			"rasterizedPages": jsapi.Ints(cr.RasterizedPages),
			"rasterDrops":     rasterDrops,
			"lostObjects":     jsapi.Issues(cr.LostObjects),
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
