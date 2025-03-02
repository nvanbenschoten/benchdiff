// Copyright 2025 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package main

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkDummy does not measure anything meaningful, but is helpful for
// quickly iterating on UI changes to `benchdiff`, for example with the
// following invocation: go run . --old HEAD . -r '.' -d 50ms -c 25
func BenchmarkDummy(b *testing.B) {
	const numBenchmarks = 10

	for n := 0; n < numBenchmarks; n++ {
		b.Run(fmt.Sprintf("%02d", n), func(b *testing.B) {
			b.ReportAllocs()
			var cnt int
			for i := 0; i < b.N; i++ {
				sl := make([]int, 1024)
				sl[0], sl[1] = 1, int(time.Now().UnixNano())
				for j := 2; j < len(sl); j++ {
					sl[j] = sl[j-1]*sl[j-2] + 1
				}
				cnt += sl[len(sl)-1]
			}
		})
	}
}
