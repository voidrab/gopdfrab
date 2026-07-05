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
	"syscall/js"

	"github.com/voidrab/gopdfrab"
)

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
//	  issues: Array<{ clause: string, subclause: number, name: string, page: number, messages: string[] }>
//	}>
func jsVerify(_ js.Value, args []js.Value) any {
	return newPromise(func() (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("gopdfrabVerify: expected 1 argument")
		}
		data := copyBytes(args[0])
		result, err := gopdfrab.VerifyBytes(data, gopdfrab.PDFA_1B)
		if err != nil {
			return nil, fmt.Errorf("verify: %w", err)
		}

		issues := make([]any, 0, len(result.Issues))
		for _, iss := range result.Issues {
			c := iss.Check()
			msgs := iss.Messages()
			jsMsgs := make([]any, len(msgs))
			for i, m := range msgs {
				jsMsgs[i] = m
			}
			issues = append(issues, map[string]any{
				"clause":    c.Clause(),
				"subclause": c.Subclause(),
				"name":      c.Name(),
				"page":      iss.Page(),
				"messages":  jsMsgs,
			})
		}

		return map[string]any{
			"valid":   result.Valid,
			"summary": result.Summary(),
			"issues":  issues,
		}, nil
	})
}

// jsConvert implements:
//
//	gopdfrabConvert(bytes: Uint8Array) → Promise<{
//	  valid: boolean,
//	  iterations: number,
//	  output: Uint8Array,
//	  residual: Array<{ clause: string, subclause: number, name: string, page: number, messages: string[] }>
//	}>
func jsConvert(_ js.Value, args []js.Value) any {
	return newPromise(func() (any, error) {
		if len(args) < 1 {
			return nil, fmt.Errorf("gopdfrabConvert: expected 1 argument")
		}
		data := copyBytes(args[0])
		cr, err := gopdfrab.ConvertBytes(data, gopdfrab.PDFA_1B)
		if err != nil {
			return nil, fmt.Errorf("convert: %w", err)
		}

		residual := cr.Residual()
		jsResidual := make([]any, 0, len(residual))
		for _, iss := range residual {
			c := iss.Check()
			msgs := iss.Messages()
			jsMsgs := make([]any, len(msgs))
			for i, m := range msgs {
				jsMsgs[i] = m
			}
			jsResidual = append(jsResidual, map[string]any{
				"clause":    c.Clause(),
				"subclause": c.Subclause(),
				"name":      c.Name(),
				"page":      iss.Page(),
				"messages":  jsMsgs,
			})
		}

		// Copy output bytes into a JS Uint8Array.
		jsOut := js.Global().Get("Uint8Array").New(len(cr.Output))
		js.CopyBytesToJS(jsOut, cr.Output)

		return map[string]any{
			"valid":      cr.Result.Valid,
			"iterations": cr.Iterations,
			"output":     jsOut,
			"residual":   jsResidual,
		}, nil
	})
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
