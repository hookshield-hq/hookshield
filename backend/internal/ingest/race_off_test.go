//go:build !race

package ingest_test

// raceEnabled is true when tests run with -race (latency numbers are meaningless then).
const raceEnabled = false
