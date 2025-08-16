package gobreaker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountsMethods(t *testing.T) {
	counts := Counts{}

	counts.onRequest()
	assert.Equal(t, Counts{Requests: 1}, counts)

	counts.onSuccess()
	assert.Equal(t, Counts{Requests: 1, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, counts)

	counts.onRequest()
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, counts)

	counts.onSuccess()
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 2, ConsecutiveSuccesses: 2}, counts)

	counts.onRequest()
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, ConsecutiveSuccesses: 2}, counts)

	counts.onFailure()
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, counts)

	counts.onRequest()
	assert.Equal(t, Counts{Requests: 4, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, counts)

	counts.onFailure()
	assert.Equal(t, Counts{Requests: 4, TotalSuccesses: 2, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 2}, counts)

	counts.clear()
	assert.Equal(t, Counts{}, counts)
}

func TestNewRollingCounts(t *testing.T) {
	rc := newRollingCounts(-1)
	assert.Equal(t, uint64(0), rc.age)
	assert.Equal(t, 0, len(rc.buckets))
	assert.Equal(t, Counts{}, rc.Counts)

	rc = newRollingCounts(0)
	assert.Equal(t, uint64(0), rc.age)
	assert.Equal(t, 0, len(rc.buckets))
	assert.Equal(t, Counts{}, rc.Counts)

	rc = newRollingCounts(5)
	assert.Equal(t, uint64(0), rc.age)
	assert.Equal(t, 5, len(rc.buckets))
	assert.Equal(t, Counts{}, rc.Counts)
	for i := range rc.buckets {
		assert.Equal(t, Counts{}, rc.buckets[i])
	}
}

func TestRollingCountsIndex(t *testing.T) {
	rc := newRollingCounts(0)
	assert.Equal(t, uint64(0), rc.index(0))
	assert.Equal(t, uint64(0), rc.index(1))

	rc = newRollingCounts(5)
	assert.Equal(t, uint64(0), rc.index(0))
	assert.Equal(t, uint64(1), rc.index(1))
	assert.Equal(t, uint64(2), rc.index(2))
	assert.Equal(t, uint64(3), rc.index(3))
	assert.Equal(t, uint64(4), rc.index(4))
	assert.Equal(t, uint64(0), rc.index(5))
	assert.Equal(t, uint64(1), rc.index(6))
}

func TestRollingCountsCurrent(t *testing.T) {
	rc := newRollingCounts(0)
	assert.Equal(t, uint64(0), rc.current())
	rc.roll()
	assert.Equal(t, uint64(0), rc.current())

	rc = newRollingCounts(5)
	assert.Equal(t, uint64(0), rc.current())
	rc.roll()
	assert.Equal(t, uint64(1), rc.current())
	rc.roll()
	assert.Equal(t, uint64(2), rc.current())
	rc.roll()
	assert.Equal(t, uint64(3), rc.current())
	rc.roll()
	assert.Equal(t, uint64(4), rc.current())
	rc.roll()
	assert.Equal(t, uint64(0), rc.current())
	rc.roll()
	assert.Equal(t, uint64(1), rc.current())
}

func TestRollingCountsMethods(t *testing.T) {
	rc := newRollingCounts(2)
	assert.Equal(t, uint64(0), rc.age)
	assert.Equal(t, Counts{}, rc.Counts)
	assert.Equal(t, Counts{}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])

	rc.onRequest()
	assert.Equal(t, Counts{Requests: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 1}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])

	rc.onSuccess(0)
	assert.Equal(t, Counts{Requests: 1, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 1, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])

	rc.onRequest()
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])

	rc.onFailure(0)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])

	rc.onRequest()
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])

	rc.onSuccess(0)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.Counts)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])

	rc.roll()
	assert.Equal(t, uint64(1), rc.age)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.Counts)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])

	rc.onRequest()
	assert.Equal(t, Counts{Requests: 4, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.Counts)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{Requests: 1}, rc.buckets[rc.index(1)])

	rc.onSuccess(1)
	assert.Equal(t, Counts{Requests: 4, TotalSuccesses: 3, TotalFailures: 1, ConsecutiveSuccesses: 2, ConsecutiveFailures: 0}, rc.Counts)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 2, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{Requests: 1, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, rc.buckets[rc.index(1)])

	rc.roll()
	assert.Equal(t, uint64(2), rc.age)
	assert.Equal(t, Counts{Requests: 1, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, rc.Counts)
	assert.Equal(t, Counts{}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{Requests: 1, TotalSuccesses: 1, ConsecutiveSuccesses: 1}, rc.buckets[rc.index(1)])

	rc.clear()
	assert.Equal(t, uint64(0), rc.age)
	assert.Equal(t, Counts{}, rc.Counts)
	assert.Equal(t, Counts{}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])
}

func TestRollingCountsGrow(t *testing.T) {
	rc := newRollingCounts(2)

	rc.onRequest()
	rc.onSuccess(0)
	rc.onRequest()
	rc.onFailure(0)

	rc.grow(0) // no change
	assert.Equal(t, uint64(0), rc.age)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.grow(1)
	assert.Equal(t, uint64(1), rc.age)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{}, rc.bucketAt(0))
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(1))

	rc.grow(0) // no change
	assert.Equal(t, uint64(1), rc.age)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{}, rc.bucketAt(0))
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(1))

	rc.grow(2)
	assert.Equal(t, uint64(2), rc.age)
	assert.Equal(t, Counts{}, rc.Counts)
	assert.Equal(t, Counts{}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.onRequest()
	rc.onSuccess(0)
	rc.onRequest()
	rc.onFailure(0)

	assert.Equal(t, uint64(2), rc.age)
	assert.Equal(t, Counts{Requests: 2}, rc.Counts)
	assert.Equal(t, Counts{Requests: 2}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.onRequest()
	rc.onSuccess(3)
	rc.onRequest()
	rc.onFailure(3)

	assert.Equal(t, uint64(2), rc.age)
	assert.Equal(t, Counts{Requests: 4}, rc.Counts)
	assert.Equal(t, Counts{Requests: 4}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.onRequest()
	rc.onSuccess(1)
	rc.onRequest()
	rc.onFailure(1)

	assert.Equal(t, uint64(2), rc.age)
	assert.Equal(t, Counts{Requests: 6, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 6}, rc.bucketAt(0))
	assert.Equal(t, Counts{TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(1))

	rc.onRequest()
	rc.onSuccess(2)
	rc.onRequest()
	rc.onFailure(2)

	assert.Equal(t, uint64(2), rc.age)
	assert.Equal(t, Counts{Requests: 8, TotalSuccesses: 2, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 8, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(0))
	assert.Equal(t, Counts{TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(1))

	rc.grow(4)
	assert.Equal(t, uint64(4), rc.age)
	assert.Equal(t, Counts{}, rc.Counts)
	assert.Equal(t, Counts{}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))
}

func TestRollingCountsBucketAt(t *testing.T) {
	rc := newRollingCounts(2)
	assert.Equal(t, uint64(0), rc.age)
	assert.Equal(t, Counts{}, rc.Counts)
	assert.Equal(t, Counts{}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.onRequest()
	assert.Equal(t, Counts{Requests: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 1}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.onFailure(0)
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.onRequest()
	assert.Equal(t, Counts{Requests: 2, TotalFailures: 1, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 2, TotalFailures: 1, ConsecutiveFailures: 1}, rc.bucketAt(2))
	assert.Equal(t, Counts{}, rc.bucketAt(3))

	rc.onSuccess(0)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.Counts)
	assert.Equal(t, Counts{Requests: 2, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.bucketAt(-2))
	assert.Equal(t, Counts{}, rc.bucketAt(-3))

	rc.onRequest()
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.Counts)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 1, ConsecutiveSuccesses: 1, ConsecutiveFailures: 0}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.onFailure(0)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(0))
	assert.Equal(t, Counts{}, rc.bucketAt(1))

	rc.roll()
	assert.Equal(t, uint64(1), rc.age)
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{}, rc.bucketAt(0))
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(1))

	rc.onRequest()
	assert.Equal(t, Counts{Requests: 4, TotalSuccesses: 1, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{Requests: 1}, rc.bucketAt(0))
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(1))

	rc.onFailure(1)
	assert.Equal(t, Counts{Requests: 4, TotalSuccesses: 1, TotalFailures: 3, ConsecutiveSuccesses: 0, ConsecutiveFailures: 2}, rc.Counts)
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, rc.bucketAt(0))
	assert.Equal(t, Counts{Requests: 3, TotalSuccesses: 1, TotalFailures: 2, ConsecutiveSuccesses: 0, ConsecutiveFailures: 1}, rc.bucketAt(1))

	rc.roll()
	assert.Equal(t, uint64(2), rc.age)
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, rc.Counts)
	assert.Equal(t, Counts{}, rc.bucketAt(0))
	assert.Equal(t, Counts{Requests: 1, TotalFailures: 1, ConsecutiveFailures: 1}, rc.bucketAt(1))

	rc.clear()
	assert.Equal(t, uint64(0), rc.age)
	assert.Equal(t, Counts{}, rc.Counts)
	assert.Equal(t, Counts{}, rc.buckets[rc.index(0)])
	assert.Equal(t, Counts{}, rc.buckets[rc.index(1)])
}
