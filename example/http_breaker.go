package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/influxdata/gobreaker"
)

var cb *gobreaker.CircuitBreaker

func init() {
	var st gobreaker.Settings
	st.Name = "HTTP GET"
	st.ReadyToTrip = func(counts gobreaker.Counts) bool {
		failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
		return counts.Requests >= 3 && failureRatio >= 0.6
	}

	cb = gobreaker.NewCircuitBreaker(st)
}

// Get wraps http.Get in CircuitBreaker.
func Get(url string) (b []byte, err error) {
	err = cb.Execute(func() error {
		resp, err := http.Get(url)
		if err != nil {
			return err
		}

		defer resp.Body.Close()
		b, err = ioutil.ReadAll(resp.Body)
		return err
	})
	if err != nil {
		return nil, err
	}

	return b, nil
}

func main() {
	body, err := Get("http://www.google.com/robots.txt")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(body))
}
