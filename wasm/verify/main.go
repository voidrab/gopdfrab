//go:build js && wasm

// Command verify is the verify-only browser build: a syscall/js wrapper that
// exposes VerifyBytes as an awaitable JavaScript function on the global
// object. It is built on gopdfrab/verify and links none of the converter, so
// it is less than half the size of the full module in ./wasm; a host can load
// it first and fetch the full module only when a conversion is asked for.
//
// Build with:
//
//	GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o gopdfrab-verify.wasm ./wasm/verify
package main

import (
	"syscall/js"

	"github.com/voidrab/gopdfrab/wasm/internal/jsapi"
)

func main() {
	js.Global().Set("gopdfrabVerify", js.FuncOf(jsapi.Verify))

	// Signal readiness to the worker host.
	jsapi.Ready("verify")

	// Block forever to keep the WASM instance alive.
	select {}
}
