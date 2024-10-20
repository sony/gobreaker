package circuit_breaker

import (
	"github.com/github.com/jorelcb/golang-circuit-breaker/pkg/circuit_breaker/domain/model/entities"
	"time"
)

const (
	circuitBreakerName = "default-name"
)

type FeatureContext struct {
	//suite.Suite
	name           string
	maxRequests    uint32
	interval       time.Duration
	timeout        time.Duration
	circuitBreaker entities.CircuitBreaker
	counts         entities.RequestsCounts
	zeroCounts     entities.RequestsCounts
	options        []entities.Option
	err            error
}

func (c *FeatureContext) SetupSuite() {
	c.zeroCounts = entities.RequestsCounts{}
}

func (c *FeatureContext) SetupTest() {

}
