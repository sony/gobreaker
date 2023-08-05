package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/commitsmart/gobreaker"
	"github.com/commitsmart/gobreaker/domain"
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
func Get(url string) ([]byte, *domain.ErrorMessageBody, error) {
	body, domainErr, err := cb.Execute(func() ([]byte, *domain.ErrorMessageBody, error) {
		resp, err := http.Get(url)
		if err != nil {
			return nil, nil, err
		}

		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, &domain.ErrorMessageBody{}, err
		}

		return body, nil, nil
	})
	if err != nil {
		return nil, nil, err
	}

	return body, domainErr, nil
}

// GetWithCustomError wraps http.Get in CircuitBreaker.
func GetWithCustomError(url string) ([]byte, *domain.ErrorMessageBody, error) {
	body, domainErr, err := cb.ExecuteWithCustomError(func() ([]byte, *domain.ErrorMessageBody, error) {
		resp, err := http.Get(url)
		if err != nil {
			return nil, nil, err
		}

		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return nil, &domain.ErrorMessageBody{}, err
		}

		return body, nil, nil
	})
	if err != nil {
		return nil, nil, err
	}

	return body, domainErr, nil
}

func main() {
	body, _, err := Get("http://www.google.com/robots.txt")
	if err != nil {
		log.Fatal(err)
	}

	body, _, err = GetWithCustomError("http://www.google.com/robots.txt")
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(string(body))
}
