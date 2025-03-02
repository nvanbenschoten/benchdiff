// Copyright 2018 The Cockroach Authors.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.txt.
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0, included in the file
// licenses/APL.txt.

package ui

import (
	"fmt"
	"io"
	"strings"
)

// Writer is a wrapper around an io.Writer which keeps track of the line count
// and can be used to issue escape sequences to clear lines.
//
// The "mark" of the current position can be saved using GetMark() and later all
// lines since that mark can be erased using ClearToMark(). Multiple marks can
// be used; see TestWriter for an example.
type Writer struct {
	wrapped io.Writer

	// lineIdx is the index of the current line (since the writer was created).
	lineIdx int
}

func NewWriter(wrapped io.Writer) *Writer {
	return &Writer{wrapped: wrapped}
}

type Mark struct {
	lineIdx int
}

func (w *Writer) Write(b []byte) (n int, err error) {
	n, err = w.wrapped.Write(b)
	for _, c := range b[:n] {
		if c == '\n' {
			w.lineIdx++
		}
	}
	return n, err
}

// GetMark returns a Mark representing the current line. Later, ClearToMark()
// can be used with this mark to erase all lines since this mark.
func (w *Writer) GetMark() Mark {
	return Mark{lineIdx: w.lineIdx}
}

// ClearToMark issues escape sequences to erase all lines since the given mark.
func (w *Writer) ClearToMark(m Mark) {
	if w.lineIdx < m.lineIdx {
		panic("invalid use of mark (marked line was cleared)")
	}
	fmt.Fprint(w.wrapped, strings.Repeat("\033[1A\033[2K\r", w.lineIdx-m.lineIdx))
	w.lineIdx = m.lineIdx
}
