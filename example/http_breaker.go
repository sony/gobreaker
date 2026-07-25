package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/sony/gobreaker"
)

var cb *gobreaker.CircuitBreaker

// StatusError is returned when the server responds with a non-2xx status code.
type StatusError struct {
	Code   int
	Status string
}

func (e *StatusError) Error() string {
	return "unexpected status: " + e.Status
}

func init() {
	var st gobreaker.Settings
	st.Name = "HTTP GET"
	st.ReadyToTrip = func(counts gobreaker.Counts) bool {
		failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
		return counts.Requests >= 3 && failureRatio >= 0.6
	}
	st.IsSuccessful = func(err error) bool {
		if err == nil {
			return true
		}
		// Server errors indicate the service is unhealthy and should trip
		// the circuit breaker. Client errors such as 404 Not Found are
		// responses the server produced deliberately, so they are returned
		// to the caller without counting as failures.
		var se *StatusError
		return errors.As(err, &se) && se.Code < http.StatusInternalServerError
	}

	cb = gobreaker.NewCircuitBreaker(st)
}

// Get wraps http.Get in CircuitBreaker.
func Get(url string) ([]byte, error) {
	body, err := cb.Execute(func() (interface{}, error) {
		resp, err := http.Get(url)
		if err != nil {
			return nil, err
		}

		defer resp.Body.Close()

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, &StatusError{Code: resp.StatusCode, Status: resp.Status}
		}

		return ioutil.ReadAll(resp.Body)
	})
	if err != nil {
		return nil, err
	}

	return body.([]byte), nil
}

func main() {
	body, err := Get("http://www.google.com/robots.txt")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(body))
}
