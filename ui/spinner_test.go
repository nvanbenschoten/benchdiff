package ui

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestSpinner(t *testing.T) {
	if !testing.Verbose() {
		// This is a visual test that can only be run in verbose mode.
		return
	}
	w := NewWriter(os.Stdout)
	s := StartSpinner(w, "prefix")
	for i := 1; i <= 4; i++ {
		time.Sleep(1 * time.Second)
		s.Update(fmt.Sprintf(" %ds", i))
	}
	s.Stop()
}
