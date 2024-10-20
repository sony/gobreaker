# Offline Payments Circuit Breaker (mpcs-op-circuit-breaker)

## Description

> **_v1.0.0:_** Offline Payments Circuit Breaker

## Installation

`go get github.com/github.com/jorelcb/golang-circuit-breaker`

By default, `go get` will bring in the latest tagged release version of the Library.

```shell
go get github.com/github.com/jorelcb/golang-circuit-breaker/v1.0.1
```
To get a specific release version of the Library use `@<tag>` in your `go get` command.

```shell
go get github.com/github.com/jorelcb/golang-circuit-breaker@1.1.0
```

To get the latest SDK repository change use `@latest`.

```shell
go get github.com/github.com/jorelcb/golang-circuit-breaker@latest
```

## Getting started

By default, you can use the library with the default configuration, which is:

```go
    cb := circuitbreaker.NewCircuitBreaker()
```

### Library configuration

This library uses functional options to configure the circuit breaker:

```go
    cb := circuitbreaker.NewCircuitBreaker(
		circuitbreaker.WithName("my-circuit-breaker"),
        circuitbreaker.WithMaxConsecutiveFailures(3),
        circuitbreaker.WithInterval(time.Duration(10) * time.Second),
		circuitbreaker.WithTimeout(time.Duration(60) * time.Second),
		circuitbreaker.WithIsSuccessful(func(err error) bool {
			return err == nil
        }),
        circuitbreaker.WithOnStateChange(func(state circuitbreaker.State) {
            log.Printf("state changed to %s", state)
        }),
		circuitbreaker.WithReadyToTrip(func(counts circuitbreaker.Counts) bool {
			return counts.ConsecutiveFailures > 3
        })
    )
```

## Usage

Next example try by default to write a file, if it fails, it will write a log.
Circuit breaker will be open if it fails 3 times consecutively, and will be closed after 10 seconds.


```go
var breaker *entities.CircuitBreaker
breaker = NewCircuitBreaker()
```

Default options for circuit breaker are:

```go
Name:               "",
MaxRequests:        1,
Interval:           time.Duration(0) * time.Second,
Timeout:            time.Duration(60) * time.Second,
ReadyToTrip:        func (counts RequestsCounts) bool { return counts.ConsecutiveFailures > 5 },
isSuccessful:       func (err error) bool { return err == nil },

```
### Config Options

**Name** is the name of the CircuitBreaker.

**MaxRequests** is the maximum number of requests allowed to pass through
when the CircuitBreaker is half-open.
If MaxRequests is 0, the CircuitBreaker allows only 1 request.

**Interval** is the cyclic period of the closed current
for the CircuitBreaker to Clear the internal Counts.
If Interval is less than or equal to 0, the CircuitBreaker doesn't Clear internal Counts during the closed current.

**Timeout** is the period of the open current,
after which the current of the CircuitBreaker becomes half-open.
If Timeout is less than or equal to 0, the timeout value of the CircuitBreaker is set to 60 seconds.

**ReadyToTrip** is called with a copy of Counts whenever a request fails in the closed current.
If ReadyToTrip returns true, the CircuitBreaker will be placed into the open current.
If ReadyToTrip is nil, default ReadyToTrip is used.
Default ReadyToTrip returns true when the number of consecutive failures is more than 5.

**OnStateChange** is called whenever the current of the CircuitBreaker changes.

**IsSuccessful** is called with the error returned from a request.
If IsSuccessful returns true, the error is counted as a success.
Otherwise, the error is counted as a failure.
If IsSuccessful is nil, default IsSuccessful is used, which returns false for all non-nil errors.

```go
func (a *App) circuit(ctx context.Context, message string) error {
    // Configura el Circuit Breaker con una política predeterminada
    
    // Función wrapper para envolver la función writeToFile dentro del Circuit Breaker
    writeWithCircuitBreaker := func(message string) error {
        _, err := breaker.Execute(func() (interface{}, error) {
            return nil, a.writeToFile(message)
        })
        return err
    }

    err := writeWithCircuitBreaker(message)
    if err != nil {
        // En caso de fallo, redirigir hacia la función writeToLog
        err = a.writeToLog(message)
        if err != nil {
            a.l.Printf("Error al escribir en logger: %v\n", err.Error())
        }
    }
    return err
}

func (a *App) writeToFile(message string) error {
    // Implementa la lógica para escribir en Redis aquí
    // ...
    a.backend = "File"
    err := a.write("./file_backend.txt", []byte(message))
    if err != nil {
        a.l.Printf("Error escribiendo en Archivo: %v\n", err.Error())
        return err
    }
    a.l.Printf("Mensaje escrito en archivo exitosamente.")
    return err
}

func (a *App) writeToLog(message string) error {
    // Implementa la lógica para escribir en la base de datos aquí
    a.backend = "Logger"
    a.l.Printf("Mensaje escrito en backup logger exitosamente.")
    return nil
}

func (a *App) write(name string, data []byte) error {
    f, err := os.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 644)
    if err != nil {
        return err
    }
    _, err = f.Write(data)
    if err1 := f.Close(); err1 != nil && err == nil {
        err = err1
    }
    return err
}
```

Also You can initialize custom circuit breaker using functional options:

```go
    customReadyToTrip: func(counts RequestsCounts) bool {
        numReqs := counts.Requests
        failureRatio := float64(counts.TotalFailures) / float64(numReqs)
        
        counts.Clear() // no effect on circuit breaker counts
        return numReqs >= 3 && failureRatio >= 0.6
    }
	
    cb := circuitbreaker.NewCircuitBreaker(
        circuitbreaker.WithMaxConsecutiveFailures(3),
        circuitbreaker.WithOpenTimeout(10*time.Second),
        circuitbreaker.WithHalfOpenTimeout(10*time.Second),
        circuitbreaker.WithClosedTimeout(10*time.Second),
        circuitbreaker.WithOnStateChange(func(state circuitbreaker.State) {
            log.Printf("state changed to %s", state)
        }),
		circuitbreaker.WithReadyToTrip(customReadyToTrip)
    )
```

### @TODO
- Migrate tests to BDD

> **_NOTE:_** Add code snippets

***

## Changelog

We keep changes to our codebase [here](CHANGELOG.md)

## Library Versioning

Go modules works correctly if people follow [`Semver`](https://semver.org/). Please follow semver as good as possible to simplify the job of other developers when updating projects that depends on your library.

When releasing a v2+ version, consider the requirements of `go.mod` and update your module name accordingly.

## Dependency Management

The proposed method for managing dependencies is `go mod`.

Even though `go mod` allows you to have several modules defined in the same repository we recommend you avoid doing it.

The recommended pattern is to have a single `go.mod / go.sum` in the root folder of the repository, in which all packages dependencies are specified.

### Links

* This project is public forked and evolved from [Sony GoBreaker](https://github.com/sony/gobreaker).
* [Using Go Modules](https://blog.golang.org/using-go-modules).
* [Migrating to Go Modules](https://blog.golang.org/migrating-to-go-modules).
