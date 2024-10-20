package entities

import "time"

const DefaultInterval = time.Duration(0) * time.Second
const DefaultTimeout = time.Duration(60) * time.Second
const DefaultMaxRequests = 1

// config configures CircuitBreaker:
//
// Name is the name of the CircuitBreaker.
//
// MaxRequests is the maximum number of requests allowed to pass through
// when the CircuitBreaker is half-open.
// If MaxRequests is 0, the CircuitBreaker allows only 1 request.
//
// Interval is the cyclic period of the closed current
// for the CircuitBreaker to Clear the internal Counts.
// If Interval is less than or equal to 0, the CircuitBreaker doesn't Clear internal Counts during the closed current.
//
// Timeout is the period of the open current,
// after which the current of the CircuitBreaker becomes half-open.
// If Timeout is less than or equal to 0, the timeout value of the CircuitBreaker is set to 60 seconds.
//
// ReadyToTrip is called with a copy of Counts whenever a request fails in the closed current.
// If ReadyToTrip returns true, the CircuitBreaker will be placed into the open current.
// If ReadyToTrip is nil, default ReadyToTrip is used.
// Default ReadyToTrip returns true when the number of consecutive failures is more than 5.
//
// OnStateChange is called whenever the current of the CircuitBreaker changes.
//
// IsSuccessful is called with the error returned from a request.
// If IsSuccessful returns true, the error is counted as a success.
// Otherwise, the error is counted as a failure.
// If IsSuccessful is nil, default IsSuccessful is used, which returns false for all non-nil errors.
type config struct {
	name          string
	maxRequests   uint32
	interval      time.Duration
	timeout       time.Duration
	readyToTrip   func(counts RequestsCounts) bool
	onStateChange func(name string, from State, to State)
	isSuccessful  func(err error) bool
}

func DefaultReadyToTrip(counts RequestsCounts) bool {
	return counts.ConsecutiveFailures > 5
}

func DefaultIsSuccessful(err error) bool {
	return err == nil
}

func newConfig(options []Option) *config {
	conf := &config{
		name:          "",
		maxRequests:   DefaultMaxRequests,
		interval:      DefaultInterval,
		timeout:       DefaultTimeout,
		readyToTrip:   DefaultReadyToTrip,
		onStateChange: nil,
		isSuccessful:  DefaultIsSuccessful,
	}

	for _, opt := range options {
		opt.apply(conf)
	}

	return conf
}

//------------------------------------------------------------------------------

type Option interface {
	apply(c *config)
}

type option func(conf *config)

func (fn option) apply(conf *config) {
	fn(conf)
}

func WithName(name string) Option {
	return option(func(conf *config) {
		conf.name = name
	})
}

func WithMaxRequests(maxRequests uint32) Option {
	if maxRequests == 0 {
		maxRequests = DefaultMaxRequests
	}
	return option(func(conf *config) {
		conf.maxRequests = maxRequests
	})
}

func WithInterval(interval time.Duration) Option {
	if interval <= 0 {
		interval = DefaultInterval
	}
	return option(func(conf *config) {
		conf.interval = interval
	})
}

func WithTimeout(timeout time.Duration) Option {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return option(func(conf *config) {
		conf.timeout = timeout
	})
}

func WithReadyToTrip(readyToTrip func(counts RequestsCounts) bool) Option {
	return option(func(conf *config) {
		conf.readyToTrip = readyToTrip
	})
}

func WithOnStateChange(onStateChange func(name string, from State, to State)) Option {
	return option(func(conf *config) {
		conf.onStateChange = onStateChange
	})
}

func WithIsSuccessful(isSuccessful func(err error) bool) Option {
	return option(func(conf *config) {
		conf.isSuccessful = isSuccessful
	})
}
