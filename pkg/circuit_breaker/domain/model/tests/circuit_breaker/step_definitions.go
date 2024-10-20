package circuit_breaker

import "github.com/cucumber/godog"

var featureContext = new(FeatureContext)

func InitializeTestSuite(ctx *godog.TestSuiteContext) {
	ctx.BeforeSuite(featureContext.SetupTest)
}

func InitializeScenario(ctx *godog.ScenarioContext) {
	// Given steps

	// When steps

	// Then steps
}
