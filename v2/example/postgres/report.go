package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/sony/gobreaker/v2"
)

// request is one purchase as the caller saw it.
type request struct {
	Arrived  time.Duration // since the start
	Finished time.Duration // when Postgres answered or the timeout hit; 0 if the breaker rejected it
	Result   result
}

type result int

const (
	succeeded result = iota
	timedOut
	failed // with another error
	rejectedOpen
	rejectedHalfOpen
	rejectedRecovering
)

func classify(err error) result {
	switch {
	case err == nil || errors.Is(err, errLimitReached):
		return succeeded
	case errors.Is(err, gobreaker.ErrOpenState):
		return rejectedOpen
	case errors.Is(err, gobreaker.ErrRecovering):
		return rejectedRecovering
	case errors.Is(err, gobreaker.ErrTooManyRequests):
		return rejectedHalfOpen
	case errors.Is(err, context.DeadlineExceeded):
		return timedOut
	default:
		return failed
	}
}

// fullLoadRun is how many purchases in a row the breaker lets through when it lets the full load in.
// At any share below 1 it turns away at least one purchase in every few, so a run this long means the full load.
const fullLoadRun = 20

type stateChange struct {
	At       time.Duration
	From, To gobreaker.State
}

// recorder collects what happened during the run.
type recorder struct {
	start time.Time

	mu       sync.Mutex
	requests []request
	changes  []stateChange
	postgres map[int]pgActivity // per second: the busiest sample
	rollback [2]int64           // Postgres rollbacks at the start and at the end
}

func newRecorder() *recorder {
	return &recorder{start: time.Now(), postgres: map[int]pgActivity{}}
}

func (r *recorder) since() time.Duration { return time.Since(r.start) }

func (r *recorder) addRequest(arrived, finished time.Time, err error) {
	req := request{Arrived: arrived.Sub(r.start), Result: classify(err)}
	if !finished.IsZero() {
		req.Finished = finished.Sub(r.start)
	}
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
}

// addStateChange is called from OnStateChange, under the breaker's lock.
func (r *recorder) addStateChange(from, to gobreaker.State) {
	r.mu.Lock()
	r.changes = append(r.changes, stateChange{r.since(), from, to})
	r.mu.Unlock()
}

func (r *recorder) addPostgres(a pgActivity) {
	second := int(r.since() / time.Second)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rollback[0] == 0 {
		r.rollback[0] = a.Rollbacks
	}
	r.rollback[1] = a.Rollbacks
	old := r.postgres[second]
	r.postgres[second] = pgActivity{Busy: max(old.Busy, a.Busy), LockWaits: max(old.LockWaits, a.LockWaits), Rollbacks: a.Rollbacks}
}

// counts are the requests of a stretch of time, by result.
type counts struct {
	Offered            int `json:"offered"`
	LetThrough         int `json:"let_through"`
	Succeeded          int `json:"succeeded"`
	Failed             int `json:"failed"`    // all failures the breaker counted
	TimedOut           int `json:"timed_out"` // the failures that were timeouts
	RejectedOpen       int `json:"rejected_open"`
	RejectedHalfOpen   int `json:"rejected_half_open"`
	RejectedRecovering int `json:"rejected_recovering"`
}

func (c *counts) add(req request) {
	c.Offered++
	switch req.Result {
	case succeeded:
		c.Succeeded++
	case timedOut:
		c.Failed++
		c.TimedOut++
	case failed:
		c.Failed++
	case rejectedOpen:
		c.RejectedOpen++
	case rejectedHalfOpen:
		c.RejectedHalfOpen++
	case rejectedRecovering:
		c.RejectedRecovering++
	}
	if req.Finished > 0 {
		c.LetThrough++
	}
}

// closing is one stretch from a half-open-to-closed transition to the next trip.
type closing struct {
	At        float64 `json:"closed_at_s"`
	TripAfter float64 `json:"trip_after_s"` // -1 if it did not trip
	counts
	// PeakShare is the largest share of requests let through in any 250 ms of the stretch.
	PeakShare float64 `json:"peak_share"`
	// LongestRun is the most requests in a row, by arrival, that the breaker let through.
	LongestRun int `json:"longest_run"`
	// FirstFailure is when the first request let through after closing failed, since closing.
	FirstFailure float64 `json:"first_failure_s"`
	// TripDelay is the time from that first failure to the trip.
	TripDelay float64 `json:"trip_delay_s"`
	// LetThroughAfterFailure counts the requests let through between the first failure and the trip.
	LetThroughAfterFailure int `json:"let_through_after_failure"`
	// InFlightAtTrip counts the requests let through that had no answer yet at the trip:
	// running in Postgres or waiting for one of the pool's connections.
	InFlightAtTrip int `json:"in_flight_at_trip"`
}

// incident summarizes the time without the index.
type incident struct {
	counts
	Trips       int        `json:"trips"`
	Floods      int        `json:"floods"`
	Closings    []closing  `json:"closings"`
	SucceededMs [2]float64 `json:"succeeded_p50_p99_ms"`
	PgBusyMax   int        `json:"pg_busy_max"`
	PgLockMax   int        `json:"pg_lock_waits_max"`
	PgRollbacks int64      `json:"pg_rollbacks"`
	// FullLoadAfter is how long after the index came back every purchase went through again.
	FullLoadAfter float64 `json:"full_load_after_s"`
	// TurnedAway counts the purchases the breaker rejected after the index came back.
	TurnedAway int `json:"turned_away_after_fix"`
}

func (r *recorder) summarize(from, to time.Duration) incident {
	r.mu.Lock()
	defer r.mu.Unlock()

	var inc incident
	var latencies []float64
	for _, req := range r.requests {
		if req.Arrived < from || req.Arrived >= to {
			continue
		}
		inc.add(req)
		if req.Result == succeeded {
			latencies = append(latencies, float64((req.Finished-req.Arrived).Microseconds())/1000)
		}
	}
	inc.SucceededMs = percentiles(latencies)

	for i, c := range r.changes {
		if c.At < from || c.At >= to {
			continue
		}
		if c.From == gobreaker.StateClosed && c.To == gobreaker.StateOpen {
			inc.Trips++
		}
		if c.From == gobreaker.StateHalfOpen && c.To == gobreaker.StateClosed {
			cl := r.closing(c.At, r.nextTrip(i), to)
			if cl.LongestRun >= fullLoadRun {
				inc.Floods++
			}
			inc.Closings = append(inc.Closings, cl)
		}
	}

	for second, a := range r.postgres {
		at := time.Duration(second) * time.Second
		if at >= from && at < to {
			inc.PgBusyMax = max(inc.PgBusyMax, a.Busy)
			inc.PgLockMax = max(inc.PgLockMax, a.LockWaits)
		}
	}
	inc.PgRollbacks = r.rollback[1] - r.rollback[0]

	inc.FullLoadAfter = -1
	for at := to; at < r.end(); at += time.Second / 4 {
		if r.allThrough(at, at+time.Second) {
			inc.FullLoadAfter = (at - to).Seconds()
			break
		}
	}
	for _, req := range r.requests {
		if req.Arrived >= to && req.Result >= rejectedOpen {
			inc.TurnedAway++
		}
	}
	return inc
}

func (r *recorder) end() time.Duration {
	if len(r.requests) == 0 {
		return 0
	}
	return r.requests[len(r.requests)-1].Arrived
}

func (r *recorder) nextTrip(i int) time.Duration {
	for _, c := range r.changes[i+1:] {
		if c.To == gobreaker.StateOpen {
			return c.At
		}
	}
	return -1
}

func (r *recorder) closing(at, trip, end time.Duration) closing {
	cl := closing{At: at.Seconds(), TripAfter: -1}
	until := end
	if trip >= 0 {
		cl.TripAfter = (trip - at).Seconds()
		until = trip
	}

	firstFailure := time.Duration(-1)
	var arrivals []request
	for _, req := range r.requests {
		if req.Arrived < at || req.Arrived >= until {
			continue
		}
		cl.add(req)
		arrivals = append(arrivals, req)
		if (req.Result == timedOut || req.Result == failed) && (firstFailure < 0 || req.Finished < firstFailure) {
			firstFailure = req.Finished
		}
		if trip >= 0 && req.Finished > trip {
			cl.InFlightAtTrip++
		}
	}
	if firstFailure >= 0 && firstFailure <= until {
		cl.FirstFailure = (firstFailure - at).Seconds()
		cl.TripDelay = (until - firstFailure).Seconds()
		for _, req := range r.requests {
			if req.Arrived >= firstFailure && req.Arrived < until && req.Finished > 0 {
				cl.LetThroughAfterFailure++
			}
		}
	}

	// Requests are recorded as they finish; the run counts them as they arrived.
	slices.SortFunc(arrivals, func(a, b request) int { return cmp.Compare(a.Arrived, b.Arrived) })
	run := 0
	for _, req := range arrivals {
		if req.Finished > 0 {
			run++
			cl.LongestRun = max(cl.LongestRun, run)
		} else {
			run = 0
		}
	}

	for w := at; w+time.Second/4 <= until; w += time.Second / 20 {
		var c counts
		for _, req := range r.requests {
			if req.Arrived >= w && req.Arrived < w+time.Second/4 {
				c.add(req)
			}
		}
		if c.Offered > 0 {
			cl.PeakShare = max(cl.PeakShare, float64(c.LetThrough)/float64(c.Offered))
		}
	}
	return cl
}

func (r *recorder) allThrough(from, to time.Duration) bool {
	n := 0
	for _, req := range r.requests {
		if req.Arrived >= from && req.Arrived < to {
			if req.Result != succeeded {
				return false
			}
			n++
		}
	}
	return n > 0
}

func percentiles(values []float64) [2]float64 {
	if len(values) == 0 {
		return [2]float64{}
	}
	slices.Sort(values)
	return [2]float64{values[len(values)/2], values[min(len(values)*99/100, len(values)-1)]}
}

// printTimeline prints one line per second.
func (r *recorder) printTimeline(w io.Writer) {
	r.mu.Lock()
	defer r.mu.Unlock()

	seconds := int(r.end()/time.Second) + 1
	rows := make([]counts, seconds)
	latencies := make([][]float64, seconds)
	for _, req := range r.requests {
		s := int(req.Arrived / time.Second)
		rows[s].add(req)
		if req.Result == succeeded {
			latencies[s] = append(latencies[s], float64((req.Finished-req.Arrived).Microseconds())/1000)
		}
	}

	fmt.Fprintf(w, "%5s  %-10s  %7s  %7s  %5s  %6s  %8s  %6s  %7s  %7s  %s\n",
		"time", "phase", "offered", "let in", "ok", "failed", "rejected", "ok p99", "pg busy", "pg wait", "breaker")
	for s, c := range rows {
		var changes []string
		for _, ch := range r.changes {
			if int(ch.At/time.Second) == s {
				changes = append(changes, fmt.Sprintf("%.2fs %s", ch.At.Seconds(), ch.To))
			}
		}
		pg := r.postgres[s]
		fmt.Fprintf(w, "%4ds  %-10s  %7d  %7d  %5d  %6d  %8d  %5.0fms  %7d  %7d  %v\n",
			s, phaseAt(time.Duration(s)*time.Second), c.Offered, c.LetThrough, c.Succeeded, c.Failed,
			c.RejectedOpen+c.RejectedHalfOpen+c.RejectedRecovering, percentiles(latencies[s])[1],
			pg.Busy, pg.LockWaits, changes)
	}
}

func (inc incident) print(w io.Writer) {
	fmt.Fprintf(w, "\nWithout the index (%d purchases offered):\n", inc.Offered)
	fmt.Fprintf(w, "  let through to Postgres  %6d  (succeeded %d, failed %d: timed out %d, other errors %d)\n",
		inc.LetThrough, inc.Succeeded, inc.Failed, inc.TimedOut, inc.Failed-inc.TimedOut)
	fmt.Fprintf(w, "  rejected by the breaker  %6d  (open %d, half-open %d, recovering %d)\n",
		inc.RejectedOpen+inc.RejectedHalfOpen+inc.RejectedRecovering, inc.RejectedOpen, inc.RejectedHalfOpen, inc.RejectedRecovering)
	fmt.Fprintf(w, "  trips %d; closings that let the full load in (%d purchases in a row): %d of %d\n",
		inc.Trips, fullLoadRun, inc.Floods, len(inc.Closings))
	fmt.Fprintf(w, "  succeeded purchases took p50 %.0f ms, p99 %.0f ms\n", inc.SucceededMs[0], inc.SucceededMs[1])
	fmt.Fprintf(w, "  Postgres: at most %d purchases busy, %d of them waiting for an event row; %d transactions rolled back\n",
		inc.PgBusyMax, inc.PgLockMax, inc.PgRollbacks)
	fmt.Fprintf(w, "\nEach time the breaker closed without the index:\n")
	fmt.Fprintf(w, "  %8s  %7s  %5s  %6s  %10s  %11s  %13s  %12s  %15s  %17s\n",
		"closed", "let in", "ok", "failed", "peak share", "longest run", "first failure", "trip after it", "let in after it", "in flight at trip")
	for _, c := range inc.Closings {
		trip := "no trip"
		if c.TripAfter >= 0 {
			trip = fmt.Sprintf("%.0f ms", c.TripDelay*1000)
		}
		fmt.Fprintf(w, "  %7.1fs  %7d  %5d  %6d  %9.0f%%  %11d  %12.2fs  %12s  %15d  %17d\n",
			c.At, c.LetThrough, c.Succeeded, c.Failed, c.PeakShare*100, c.LongestRun, c.FirstFailure, trip, c.LetThroughAfterFailure, c.InFlightAtTrip)
	}
	fmt.Fprintf(w, "\nAfter the index came back: every purchase went through again after %.2fs; %d purchases rejected meanwhile.\n",
		inc.FullLoadAfter, inc.TurnedAway)
}
