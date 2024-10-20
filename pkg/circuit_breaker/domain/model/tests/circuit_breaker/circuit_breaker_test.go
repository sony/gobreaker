package circuit_breaker

import (
	"github.com/cucumber/godog"
	"github.com/github.com/jorelcb/golang-circuit-breaker/pkg/circuit_breaker/domain/model/tests/commons"
	"testing"
)

func TestEventFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                 "circuit_breaker",
		TestSuiteInitializer: InitializeTestSuite,
		ScenarioInitializer:  InitializeScenario,
		Options:              commons.Options("./circuit_breaker.feature"),
	}

	if suite.Run() != 0 {
		t.Fail()
	}
}
