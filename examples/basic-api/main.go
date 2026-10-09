package main

import (
	"fmt"
	"log"
	"net/http"

	"github.com/dayanchm/golar/http/router"
)

func main() {
	app := router.New()

	app.Get("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Welcome to Golar!")
	})

	app.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		fmt.Fprintf(w, "User ID: %s", id)
	})

	log.Println("Golar running at http://localhost:8080")

	log.Fatal(http.ListenAndServe(":8080", app))
}
