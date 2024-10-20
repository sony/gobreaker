package _examples

import (
	"context"
	"github.com/github.com/jorelcb/golang-circuit-breaker/pkg/circuit_breaker/domain/model/entities"
	"github.com/gofiber/fiber/v2"
	"log"
	"net/http"
	"os"
	"time"
)

type App struct {
	l        *log.Logger
	backend  string
	counter  uint64
	switcher bool
}

type response struct {
	Value   string `json:"value"`
	Backend string `json:"backend"`
}

func NewApp(l *log.Logger) *App {
	return &App{l: l}
}

var breaker *entities.CircuitBreaker

func (a *App) Run() error {
	var err error
	//webApp := web.New()
	fiberApp := fiber.New()

	//client := &http.Client{}

	readyToTrip := func(counts entities.RequestsCounts) bool {
		return counts.ConsecutiveFailures > 10
	}
	onStateChange := func(name string, from entities.State, to entities.State) {
		a.l.Printf("Circuit breaker state change: %s, from %s to %s", name, from.Name(), to.Name())
	}

	breaker = entities.NewCircuitBreaker(
		entities.WithName("MyCircuitBreaker"),
		entities.WithMaxRequests(10),
		entities.WithInterval(60*time.Second),
		entities.WithTimeout(30*time.Second),
		entities.WithReadyToTrip(readyToTrip),
		entities.WithOnStateChange(onStateChange),
	)

	fiberApp.Post("/circuit", func(c *fiber.Ctx) error {
		value := c.Body()
		c.Query("value")
		if string(value) == "" {
			return c.Status(http.StatusBadRequest).SendString("param value cannot be empty")
		}
		err := a.circuit(c.Context(), string(value))

		if err != nil {
			return err
		}

		resp := response{
			Value:   "backend",
			Backend: a.backend,
		}
		return c.JSON(resp)
	})

	log.Fatal(fiberApp.Listen(":3000"))

	//webApp.Get("/circuit", func(w http.ResponseWriter, r *http.Request) error {
	//	value := r.URL.Query().Get("value")
	//	if value == "" {
	//		return web.NewError(http.StatusBadRequest, "param value cannot be empty")
	//	}
	//	err := a.circuit(r.Context(), value)
	//
	//	if err != nil {
	//		return err
	//	}
	//
	//	resp := response{
	//		Value:   "backend",
	//		Backend: a.backend,
	//	}
	//	return web.EncodeJSON(w, resp, http.StatusOK)
	//})

	// Create the listener that will be pass in to the underlying http.Server for attending incoming requests
	//ln, err := net.Listen("tcp", ":8080")
	//if err != nil {
	//	log.Fatal(err)
	//	return err
	//}
	//if err := web.Run(ln, web.DefaultTimeouts, webApp); err != nil {
	//	log.Fatal(err)
	//	return err
	//}
	return err
}

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
	//a.l.Printf("Intentando en Archivo. ", a.counter, message)
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
	// ...
	a.backend = "Logger"
	//a.l.Printf("Intentando en Logger. ", a.counter, message)
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
