// The benchmarks subproject is a separate module so its scripts, tools, and
// results stay out of the published github.com/voidrab/gopdfrab module zip.
module github.com/voidrab/gopdfrab/benchmarks

go 1.26.4

require github.com/voidrab/gopdfrab v0.0.0

require github.com/klauspost/compress v1.19.0 // indirect

replace github.com/voidrab/gopdfrab => ../
