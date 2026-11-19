//go:build race

package main

// raceEnabled says the tests were built with -race, which slows real-time
// WebRTC enough that the end-to-end tests allow it longer.
const raceEnabled = true
