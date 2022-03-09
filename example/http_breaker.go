package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/rocyou/gobreaker"
)

var cb *gobreaker.CircuitBreaker

func init() {
	var st gobreaker.Settings
	st.Name = "HTTP GET"
	st.ReadyToTrip = func(counts gobreaker.Counts) bool {
		return counts.ConsecutiveFailures >= 3
	}
	st.Interval = time.Duration(0) * time.Second
	st.Timeout = time.Duration(3) * time.Second
	st.ReadyToClose = func(counts gobreaker.Counts) (bool, bool) {
		if counts.TotalSuccesses >= 2 {
			return true, true
		}
		var nowOpen bool
		if counts.ConsecutiveFailures >= 3 {
			nowOpen = true
		}
		return false, nowOpen
	}
	st.OnStateChange = func(name string, from gobreaker.State, to gobreaker.State) {
		fmt.Printf("change state from:%+v to:%+v\n", from, to)
		//implement your action here
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
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return body, nil
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
