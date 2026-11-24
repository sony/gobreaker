package gobreaker

import (
	"math"
	"slices"
	"time"
)

// Recovery configures gradual recovery: after the half-open state the CircuitBreaker
// lets requests back in steps instead of all at once.
//
// At each step it lets through a share of requests and rejects the rest with ErrRecovering.
// A step ends once it has lasted StepDuration and a request let through at it has succeeded.
// The first failure during recovery trips the CircuitBreaker, unless ReadyToTrip says otherwise,
// and Counts are not cleared at Interval during recovery.
// Recovery ends after the last step.
// This is not a rate limit: there is no requests-per-second ceiling.
type Recovery struct {
	// Steps are the shares of requests let through, one per step, each greater than 0 and less than 1;
	// others are ignored. If no share is valid, 0.05, 0.1, 0.25, 0.5 and 0.75 are used.
	Steps []float64

	// StepDuration is the minimum length of a step; it is 5 seconds if not positive.
	// Make it longer than the time a request takes to fail.
	StepDuration time.Duration

	// ReadyToTrip replaces Settings.ReadyToTrip during recovery.
	// If it is nil, the first failure during recovery trips the CircuitBreaker.
	ReadyToTrip func(counts Counts) bool
}

// RecoveryState is the progress of gradual recovery.
type RecoveryState struct {
	// Active is true while the CircuitBreaker is closed and recovering.
	Active bool `json:"active"`
	// Step is the current step.
	Step int `json:"step"`
	// StepStart is when the current step started.
	StepStart time.Time `json:"step_start"`
	// StepSucceeded is true once a request let through at the current step has succeeded.
	StepSucceeded bool `json:"step_succeeded"`
	// Seq numbers the requests that arrived during the current step.
	Seq uint64 `json:"seq"`
}

// notRecovering is the step of a request let through outside recovery.
const notRecovering = -1

var defaultRecoverySteps = []float64{0.05, 0.1, 0.25, 0.5, 0.75}

const defaultRecoveryStepDuration = 5 * time.Second

// goldenRatio64 is 2^64 divided by the golden ratio.
// Multiplying request numbers by it spreads them evenly over [0, 2^64),
// so any run of requests gets close to the configured share,
// and callers that take turns are let through in equal shares.
const goldenRatio64 = 0x9E3779B97F4A7C15

type recovery struct {
	shares       []float64
	thresholds   []uint64 // shares scaled to [0, 2^64)
	stepDuration time.Duration
	readyToTrip  func(counts Counts) bool
	state        RecoveryState
}

func tripOnFirstFailure(_ Counts) bool {
	return true
}

func newRecovery(r *Recovery) *recovery {
	if r == nil {
		return nil
	}

	shares := validShares(r.Steps)
	if len(shares) == 0 {
		shares = validShares(defaultRecoverySteps)
	}

	thresholds := make([]uint64, len(shares))
	for i, share := range shares {
		thresholds[i] = scaleShare(share)
	}

	stepDuration := r.StepDuration
	if stepDuration <= 0 {
		stepDuration = defaultRecoveryStepDuration
	}

	readyToTrip := r.ReadyToTrip
	if readyToTrip == nil {
		readyToTrip = tripOnFirstFailure
	}

	return &recovery{
		shares:       shares,
		thresholds:   thresholds,
		stepDuration: stepDuration,
		readyToTrip:  readyToTrip,
	}
}

// validShares returns the shares in (0, 1), sorted and without duplicates.
func validShares(steps []float64) []float64 {
	shares := make([]float64, 0, len(steps))
	for _, s := range steps {
		if s > 0 && s < 1 {
			shares = append(shares, s)
		}
	}
	slices.Sort(shares)
	return slices.Compact(shares)
}

// scaleShare scales a share to [1, 2^64), so that even the smallest share
// lets the first request at its step through.
func scaleShare(share float64) uint64 {
	scaled := share * (1 << 64)
	if scaled >= math.MaxUint64 {
		return math.MaxUint64
	}
	return max(uint64(scaled), 1)
}

// update moves to the next step once the current one has lasted StepDuration
// and a request let through at it has succeeded. After the last step it ends recovery.
func (r *recovery) update(now time.Time) {
	if !r.state.Active || !r.state.StepSucceeded || now.Sub(r.state.StepStart) < r.stepDuration {
		return
	}

	if r.state.Step+1 >= len(r.shares) {
		r.state.Active = false
		return
	}
	r.startStep(r.state.Step+1, now)
}

func (r *recovery) startStep(step int, now time.Time) {
	r.state.Step = step
	r.state.StepStart = now
	r.state.StepSucceeded = false
	r.state.Seq = 0
}

// admit decides whether a request arriving in the closed state goes through.
// It returns the step the request is let through at.
func (r *recovery) admit(now time.Time) (int, bool) {
	r.update(now)
	if !r.state.Active {
		return notRecovering, true
	}

	step := r.state.Step
	position := r.state.Seq * goldenRatio64
	r.state.Seq++
	return step, position < r.thresholds[step]
}

// onSuccess records that a request let through at the given step has succeeded.
func (r *recovery) onSuccess(step int) {
	if r.state.Active && step == r.state.Step {
		r.state.StepSucceeded = true
	}
}

func (r *recovery) onStateChange(to State, now time.Time) {
	switch to {
	case StateClosed:
		r.state.Active = true
		r.startStep(0, now)
	case StateOpen:
		r.state.Active = false
	}
}
