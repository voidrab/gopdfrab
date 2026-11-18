package cmstest

import (
	"errors"
	"testing"
)

func TestMustPanicsOnError(t *testing.T) {
	broken := errors.New("broken")
	defer func() {
		if r := recover(); r != broken {
			t.Errorf("recovered %v, want %v", r, broken)
		}
	}()
	must(0, broken)
	t.Error("must returned")
}
