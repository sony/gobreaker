package errors

const (
	tooManyRequestsError = "Too many requests"
	openStateError       = "Circuit breaker is open"
)

type circuitBreakerError struct {
	Message string
}

func (e circuitBreakerError) Error() string {
	return e.Message
}

var (
	TooManyRequestsError = circuitBreakerError{Message: tooManyRequestsError}
	OpenStateErr         = circuitBreakerError{Message: openStateError}
)
