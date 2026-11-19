package main

import (
	"sync"
	"time"
)

// A per-user token bucket for the endpoints that cost something: they reach the
// origin or the KV store, and linking in particular reports whether a publish
// token is valid, which makes an unmetered caller an oracle for guessing them.
type rateLimiter struct {
	lock    sync.Mutex
	buckets map[string]*rateBucket
	rate    float64
	burst   float64
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(burst float64, per time.Duration) *rateLimiter {
	return &rateLimiter{
		buckets: map[string]*rateBucket{},
		rate:    burst / per.Seconds(),
		burst:   burst,
	}
}

func (r *rateLimiter) allowAt(key string, now time.Time) bool {
	r.lock.Lock()
	defer r.lock.Unlock()

	bucket, known := r.buckets[key]
	if !known {
		// Prune before growing. Buckets are keyed by user id, so without this a
		// large server accumulates one permanent entry per user.
		if len(r.buckets) >= maxRateBuckets {
			for key, old := range r.buckets {
				if now.Sub(old.last) > rateBucketTTL {
					delete(r.buckets, key)
				}
			}
		}

		r.buckets[key] = &rateBucket{tokens: r.burst - 1, last: now}

		return true
	}

	// A clock put back earns nothing and costs nothing: the bucket waits
	// for it to pass the moment it was last filled to. Counted as negative
	// time, it would take tokens away, and a minute back would lock a
	// generous limiter for a minute.
	if now.After(bucket.last) {
		bucket.tokens += now.Sub(bucket.last).Seconds() * r.rate
		if bucket.tokens > r.burst {
			bucket.tokens = r.burst
		}
		bucket.last = now
	}

	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--

	return true
}

// refund gives back the token a request took, for a request that was turned
// away for a reason that was not its own: a stream still warming up, a server
// busy cutting other clips. Without it, somebody retrying the way they were
// told to would be locked out for having done so.
//
// Never past the burst, so refunds cannot bank more than a full bucket. A key
// the limiter does not hold has nothing to give back: its bucket was pruned,
// and the next request starts it full anyway.
func (r *rateLimiter) refund(key string) {
	r.lock.Lock()
	defer r.lock.Unlock()

	bucket, known := r.buckets[key]
	if !known {
		return
	}

	bucket.tokens++
	if bucket.tokens > r.burst {
		bucket.tokens = r.burst
	}
}

func (r *rateLimiter) allow(key string) bool {
	return r.allowAt(key, time.Now())
}

// wait is how long until the bucket for key will have a token, read without
// taking one: zero when one is there now. For the refusals that tell the panel
// when to try again (design/3.2.0.md §7.6); allowAt alone cannot say.
func (r *rateLimiter) wait(key string, now time.Time) time.Duration {
	r.lock.Lock()
	defer r.lock.Unlock()

	bucket, known := r.buckets[key]
	if !known {
		return 0
	}

	tokens := bucket.tokens
	if now.After(bucket.last) {
		tokens += now.Sub(bucket.last).Seconds() * r.rate
		if tokens > r.burst {
			tokens = r.burst
		}
	}

	if tokens >= 1 || r.rate <= 0 {
		return 0
	}

	wait := time.Duration((1 - tokens) / r.rate * float64(time.Second))
	if now.Before(bucket.last) {
		wait += bucket.last.Sub(now)
	}

	return wait
}
