package entities

// RequestsCounts holds the numbers of requests and their successes/failures.
// CircuitBreaker clears the internal RequestsCounts either
// on the change of the current or at the closed-current intervals.
// RequestsCounts ignores the results of the requests sent before clearing.
type RequestsCounts struct {
	Requests             uint32
	TotalSuccesses       uint32
	TotalFailures        uint32
	ConsecutiveSuccesses uint32
	ConsecutiveFailures  uint32
}

func (c *RequestsCounts) OnRequest() {
	c.Requests++
}

func (c *RequestsCounts) OnSuccess() {
	c.TotalSuccesses++
	c.ConsecutiveSuccesses++
	c.ConsecutiveFailures = 0
}

func (c *RequestsCounts) OnFailure() {
	c.TotalFailures++
	c.ConsecutiveFailures++
	c.ConsecutiveSuccesses = 0
}

func (c *RequestsCounts) Clear() {
	c.Requests = 0
	c.TotalSuccesses = 0
	c.TotalFailures = 0
	c.ConsecutiveSuccesses = 0
	c.ConsecutiveFailures = 0
}
