package main

import (
	"log"
	"net/http"

	"github.com/dayanchm/golar"
	"github.com/dayanchm/golar/http/middleware"
)

func newApp() *golar.Application {
	app := golar.New()
	app.Use(middleware.Logger(), middleware.Recovery())
	app.Get("/", func(c *golar.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"framework": "Golar", "status": "ok"})
	})
	app.Get("/health", func(c *golar.Context) error { return c.String(http.StatusOK, "ok") })
	api := app.Group("/api")
	api.Get("/users/{id}", func(c *golar.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
	})
	api.Post("/users", func(c *golar.Context) error {
		var user struct {
			Name string `json:"name"`
		}
		if err := c.BindJSON(&user); err != nil {
			return &golar.HTTPError{Code: http.StatusBadRequest, Message: "Invalid JSON", Err: err}
		}
		if user.Name == "" {
			return &golar.HTTPError{Code: http.StatusUnprocessableEntity, Message: "Name is required"}
		}
		return c.JSON(http.StatusCreated, user)
	})
	return app
}

func main() {
	app := newApp()
	log.Println("Golar listening on :8080")
	if err := app.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}
