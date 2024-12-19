package ui

import (
	"fmt"
	"io"
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
	w  Writer
}

// Start begins the Spinner, which will write all output to the provided Writer.
// All log lines delivered to the Writer will be prefixed with the specified
// prefix.
func (s *Spinner) Start(out io.Writer, prefix string) {
	if s.ch != nil {
		panic("Spinner started twice")
	}
	s.ch = make(chan string)
	s.t = time.NewTicker(100 * time.Millisecond)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.t.Stop()
		defer s.w.Flush(out)

		var progress string
		var ok bool
		var spinnerIdx int
		for {
			select {
			case <-s.t.C:
			case progress, ok = <-s.ch:
				if !ok {
					return
				}
			}
			fmt.Fprint(&s.w, prefix)
			if progress != "" {
				fmt.Fprintf(&s.w, " %s", progress)
			}
			fmt.Fprintf(&s.w, " %s\n", spinnerChars[spinnerIdx%len(spinnerChars)])
			if err := s.w.Flush(out); err != nil {
				panic(err)
			}
			spinnerIdx++
		}
	}()
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

// Fraction is a utility funcation that formats a fraction string, given a
// numerator and a denominator.
func Fraction(n, d int) string {
	dWidth := strconv.Itoa(len(strconv.Itoa(d)))
	return fmt.Sprintf("%"+dWidth+"d/%d", n, d)
}
