// startup_probe.mjs — used only by scripts/run_startup.sh to measure the
// fixed cost of Node + loading the mupdf WASM module, with no PDF
// processing. Lives here (not invoked via `node -e`) so module resolution
// finds node_modules/mupdf the same way runner.mjs does, regardless of the
// caller's working directory.
import "mupdf";
