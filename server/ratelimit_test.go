package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A refund gives back exactly the token a request took, never more than a full
// bucket, and nothing to somebody the limiter does not hold. One instant
// throughout, so nothing refills by itself in between.
func TestRefundGivesBackOneTokenUpToTheBurst(t *testing.T) {
	limiter := newRateLimiter(2, time.Hour)
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	require.True(t, limiter.allowAt("uid-a", at))
	require.True(t, limiter.allowAt("uid-a", at))
	require.False(t, limiter.allowAt("uid-a", at), "both spent")

	limiter.refund("uid-a")
	require.True(t, limiter.allowAt("uid-a", at), "the one given back")
	require.False(t, limiter.allowAt("uid-a", at))

	for range 5 {
		limiter.refund("uid-a")
	}
	require.True(t, limiter.allowAt("uid-a", at))
	require.True(t, limiter.allowAt("uid-a", at))
	require.False(t, limiter.allowAt("uid-a", at), "five refunds bank no more than a full bucket")

	// Never seen: nothing to give back, and a first request starts full
	// anyway.
	limiter.refund("uid-b")
	require.True(t, limiter.allowAt("uid-b", at))
	require.True(t, limiter.allowAt("uid-b", at))
	require.False(t, limiter.allowAt("uid-b", at))
}

// A clock put back neither costs tokens nor earns them: the bucket simply
// waits for the clock to pass the moment it was last filled to, and then
// fills from there, not from the earlier moment.
func TestAClockPutBackNeitherCostsNorEarnsTokens(t *testing.T) {
	limiter := newRateLimiter(60, time.Minute)
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	for range 30 {
		require.True(t, limiter.allowAt("uid-a", at))
	}

	// Ninety seconds back: thirty tokens are still there, not none.
	back := at.Add(-90 * time.Second)
	for range 30 {
		require.True(t, limiter.allowAt("uid-a", back))
	}
	require.False(t, limiter.allowAt("uid-a", back), "all sixty spent")

	// Forward again to the moment it was put back from: nothing has
	// refilled, since no time has passed since the bucket was last filled.
	require.False(t, limiter.allowAt("uid-a", at))
	require.True(t, limiter.allowAt("uid-a", at.Add(time.Second)), "one second on, one token")
	require.False(t, limiter.allowAt("uid-a", at.Add(time.Second)))
}
