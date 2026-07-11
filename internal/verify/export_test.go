package verify

// SetMaxWalkDepth temporarily lowers the object-graph walk depth cap and
// returns a function that restores the previous value. Test-only.
func SetMaxWalkDepth(n int) (restore func()) {
	old := maxWalkDepth
	maxWalkDepth = n
	return func() { maxWalkDepth = old }
}
