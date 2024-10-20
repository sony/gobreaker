package commons

import (
	"time"

	"github.com/cucumber/godog"
)

func Options(paths ...string) *godog.Options {
	return &godog.Options{
		Format:    "pretty",
		Paths:     paths,
		Tags:      "~@wip",
		Randomize: time.Now().UTC().UnixNano(),
		Strict:    true,
	}
}
