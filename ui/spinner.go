package ui

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

var spinnerChars = []string{"|", "/", "-", "\\"}

// Spinner coordinates the formatting of a log line with a spinner at the end.
type Spinner struct {
	ch chan string
	wg sync.WaitGroup
	t  *time.Ticker
}

// StartSpinner creates the Spinner which will write all output to the provided
// Writer. All log lines delivered to the Writer will be prefixed with the
// specified prefix.
func StartSpinner(w *Writer, prefix string) *Spinner {
	s := &Spinner{}
	if s.ch != nil {
		panic("Spinner started twice")
	}
	s.ch = make(chan string)
	s.t = time.NewTicker(100 * time.Millisecond)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.t.Stop()

		var progress string
		var ok bool
		var spinnerIdx int
		m := w.GetMark()
		for {
			select {
			case <-s.t.C:
			case progress, ok = <-s.ch:
				if !ok {
					return
				}
			}
			w.ClearToMark(m)
			fmt.Fprint(w, prefix)
			if progress != "" {
				fmt.Fprint(w, progress)
			}
			fmt.Fprintf(w, " %s\n", spinnerChars[spinnerIdx%len(spinnerChars)])
			spinnerIdx++
		}
	}()
	return s
}

// Update passes an updated progress status to the spinner.
func (s *Spinner) Update(progress string) {
	s.ch <- progress
}

// Stop closes the Spinner.
func (s *Spinner) Stop() {
	close(s.ch)
	s.wg.Wait()
}

// Fraction is a utility function that formats a fraction string, given a
// numerator and a denominator.
func Fraction(n, d int) string {
	dWidth := strconv.Itoa(len(strconv.Itoa(d)))
	return fmt.Sprintf("%"+dWidth+"d/%d", n, d)
}
