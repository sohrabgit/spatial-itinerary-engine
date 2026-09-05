package main

import "sync/atomic"

// atomic64 keeps the progress counter honest across embed workers.
type atomic64 struct{ v atomic.Int64 }

func (a *atomic64) add(n int64) int64 { return a.v.Add(n) }
