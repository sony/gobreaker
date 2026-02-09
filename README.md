gobreaker
=========

[![GoDoc](https://godoc.org/github.com/sony/gobreaker/v2?status.svg)](https://godoc.org/github.com/sony/gobreaker/v2)

[gobreaker][repo-url] implements the [Circuit Breaker pattern](https://msdn.microsoft.com/en-us/library/dn589784.aspx) in Go.

Installation
------------

```
go get github.com/sony/gobreaker/v2
```

Usage
-----

The struct `CircuitBreaker` is a state machine to prevent sending requests that are likely to fail.
The function `NewCircuitBreaker` creates a new `CircuitBreaker`.
The type parameter `T` specifies the return type of requests.

```go
func NewCircuitBreaker[T any](st Settings) *CircuitBreaker[T]
```

You can configure `CircuitBreaker` using the struct `Settings`:

```go
type Settings struct {
	Name          string
	ReadyToClose  func(counts Counts) (bool, bool)
	Interval      time.Duration
	BucketPeriod  time.Duration
	Timeout       time.Duration
	ReadyToTrip   func(counts Counts) bool
	OnStateChange func(name string, from State, to State)
	IsSuccessful  func(err error) bool
	IsExcluded    func(err error) bool
}
```

- `Name` is the name of the `CircuitBreaker`.

- `ReadyToClose` is called with a copy of `Counts` for each request in the half-open state.
  If `ReadyToClose` returns true, the `CircuitBreaker` will be placed into the close state.
  If `ReadyToClose` returns false, the `CircuitBreaker` will be placed into the open state if second returned value is true.
  If `ReadyToClose` is nil, default `ReadyToClose` is used.
  Default `ReadyToClose` returns true when the number of consecutive successes is more than 1.

- `Interval` is the cyclic period of the closed state
  for `CircuitBreaker` to clear the internal `Counts`, described later in this section.
  If `Interval` is 0, `CircuitBreaker` does not clear the internal `Counts` during the closed state.

- `BucketPeriod` defines the time duration for each bucket in the rolling window strategy.
  The internal `Counts` will be updated and reset gradually for each bucket.
  `Interval` will be automatically adjusted to be a multiple of `BucketPeriod`.
  If `BucketPeriod` is less than or equal to 0, `CircuitBreaker` will use a fixed window strategy instead.

- `Timeout` is the period of the open state,
  after which the state of `CircuitBreaker` becomes half-open.
  If `Timeout` is 0, the timeout value of `CircuitBreaker` is set to 60 seconds.

- `ReadyToTrip` is called with a copy of `Counts` whenever a request fails in the closed state.
  If `ReadyToTrip` returns true, `CircuitBreaker` will be placed into the open state.
  If `ReadyToTrip` is `nil`, the default `ReadyToTrip` is used.
  The default `ReadyToTrip` returns true when the number of consecutive failures is more than 5.

- `OnStateChange` is called whenever the state of `CircuitBreaker` changes.

- `IsSuccessful` is called with the error returned from a request.
  If `IsSuccessful` returns true, the error is counted as a success.
  Otherwise, the error is counted as a failure.
  If `IsSuccessful` is nil, the default `IsSuccessful` is used, which returns false for all non-nil errors.

- `IsExcluded` determines whether a request error should be ignored
  for the purposes of updating the circuit breaker metrics.
  If `IsExcluded` returns true for a given error,
  the request is neither counted as a success nor as a failure.
  This can be used, for example, to ignore context cancellations or
  other errors that should not affect the circuit breaker state.
  If `IsExcluded` is nil, no requests are excluded.

The struct `Counts` holds the numbers of requests and their successes/failures/exclusions:

```go
type Counts struct {
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	TotalExclusions      uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}
```

`CircuitBreaker` clears the internal `Counts` either
on the change of the state or at the closed-state intervals.
`Counts` ignores the results of the requests sent before clearing.

`CircuitBreaker` can wrap any function to send a request:

```go
func (cb *CircuitBreaker[T]) Execute(req func() (T, error)) (T, error)
```

The method `Execute` runs the given request if `CircuitBreaker` accepts it.
`Execute` returns an error instantly if `CircuitBreaker` rejects the request.
Otherwise, `Execute` returns the result of the request.
If a panic occurs in the request, `CircuitBreaker` handles it as an error
and causes the same panic again.

Example
-------

```go
var cb *gobreaker.CircuitBreaker[[]byte]

func Get(url string) ([]byte, error) {
	body, err := cb.Execute(func() ([]byte, error) {
		resp, err := http.Get(url)
		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	})
	if err != nil {
		return nil, err
	}

	return body, nil
}
```

See [example](https://github.com/sony/gobreaker/blob/master/v2/example) for details.

License
-------

The MIT License (MIT)

See [LICENSE](https://github.com/sony/gobreaker/blob/master/LICENSE) for details.


[repo-url]: https://github.com/sony/gobreaker
