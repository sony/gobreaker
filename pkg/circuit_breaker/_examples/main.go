package _examples

import (
	"log"
	"os"
)

func main() {
	l := log.New(os.Stdout, "", 0)

	app := NewApp(l)
	if err := app.Run(); err != nil {
		l.Fatal(err)
	}
}
