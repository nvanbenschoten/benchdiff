package ui

import (
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"
)

func TestWriter(t *testing.T) {
	if !testing.Verbose() {
		// This is a visual test that can only be run in verbose mode.
		return
	}
	w := NewWriter(os.Stdout)
	fmt.Fprintf(w, "line 1\n")
	fmt.Fprintf(w, "line 2\n")
	m1 := w.GetMark()
	fmt.Fprintf(w, "line 3\n")
	fmt.Fprintf(w, "line 4\n")
	for i := 0; i < 4; i++ {
		time.Sleep(1 * time.Second)
		w.ClearToMark(m1)
		k := rand.IntN(5)
		for j := 0; j <= k; j++ {
			fmt.Fprintf(w, "line %d-%d\n", i, j)
		}
	}
	w.ClearToMark(m1)
	fmt.Fprintf(w, "line 3\n")
	fmt.Fprintf(w, "line 4\n")
	m2 := w.GetMark()
	fmt.Fprintf(w, "line 5\n")
	fmt.Fprintf(w, "line 6\n")
	time.Sleep(2 * time.Second)
	w.ClearToMark(m2)
	fmt.Fprintf(w, "line 5b\n")
	time.Sleep(2 * time.Second)
	w.ClearToMark(m1)
	fmt.Fprintf(w, "done\n")
}
