package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"time"

	"github.com/sony/gobreaker"
)

var cb *gobreaker.CircuitBreaker

func init() {
	var st gobreaker.Settings
	st.Name = "HTTP GET"
	st.Timeout = time.Second * 10
	st.ReadyToTrip = func(counts gobreaker.Counts) bool {
		failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
		fmt.Printf("counts.Requests = %d, failureRatio = %v\n", counts.Requests, failureRatio)
		return counts.Requests >= 3 && failureRatio >= 0.6
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
	rand.Seed(time.Now().Unix())
	for {
		select {
		case <-time.Tick(time.Second):
			stringslice := []string{"http://www.google.com/robots.txt", "http://127.0.0.1/test.txt", "http://127.0.0.1/readme.txt"}
			idx := rand.Intn(len(stringslice))

			_, err := Get(stringslice[idx])
			if err != nil {
				log.Println(err)
			} else {
				fmt.Printf("get %s success\n", stringslice[idx])
			}

		}
	}
}
