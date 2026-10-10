// Package golar provides the application entry point for the Golar framework.
package golar

import (
	"github.com/dayanchm/golar/core"
	httpcontext "github.com/dayanchm/golar/http/context"
	"github.com/dayanchm/golar/http/middleware"
	"net/http"
)

type Application = core.Application
type Context = httpcontext.Context
type Handler = core.Handler
type ErrorHandler = core.ErrorHandler
type HTTPError = core.HTTPError
type Config = core.Config
type Hook = core.Hook
type Group = core.Group
type Middleware = middleware.Middleware

func New(config ...Config) *Application  { return core.New(config...) }
func Adapt(handler http.Handler) Handler { return core.Adapt(handler) }
