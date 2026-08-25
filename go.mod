module github.com/voidrab/gopdfrab

// 1.24 is the floor: klauspost/compress declares it too, so nothing older can
// build. Keep it patch-free -- a patch-level directive forces a toolchain
// download, and fails outright under GOTOOLCHAIN=local.
go 1.24

require github.com/klauspost/compress v1.19.0
