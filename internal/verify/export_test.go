package verify

func SetMaxWalkDepth(n int) (restore func()) {
	old := maxWalkDepth
	maxWalkDepth = n
	return func() { maxWalkDepth = old }
}
