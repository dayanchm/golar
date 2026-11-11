// Package golar provides the application entry point for the Golar framework.
//
// It re-exports the types applications use most, so a handler or bootstrap
// file usually needs only this import.
package golar

import (
	"net/http"

	"github.com/dayanchm/golar/core"
	httpcontext "github.com/dayanchm/golar/http/context"
	"github.com/dayanchm/golar/http/middleware"
	"github.com/dayanchm/golar/http/router"
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
type RouteInfo = router.RouteInfo

// Request context types, from github.com/dayanchm/golar/http/context.
type (
	// ContextConfig sets body limits and trusted proxies; pass it to
	// Application.SetContextConfig.
	ContextConfig = httpcontext.Config
	// InputError is a query, path or form value that does not parse, such
	// as from c.QueryInt; it renders as 400.
	InputError = httpcontext.InputError
	// BindError is a request body that c.Bind cannot decode; it renders as 400.
	BindError = httpcontext.BindError
	// Authorizer is a form request that checks access before validation.
	Authorizer = httpcontext.Authorizer
	// Preparer is a form request that normalizes input before validation.
	Preparer = httpcontext.Preparer
	// Template renders c.Render's views, such as *html/template.Template.
	Template = httpcontext.Template
)

var (
	// ErrForbidden denies a request with 403, for example from Authorize.
	ErrForbidden = httpcontext.ErrForbidden
	// ErrUnsupportedMediaType is returned by c.Bind for an unknown
	// Content-Type and renders as 415.
	ErrUnsupportedMediaType = httpcontext.ErrUnsupportedMediaType
)

func New(config ...Config) *Application  { return core.New(config...) }
func Adapt(handler http.Handler) Handler { return core.Adapt(handler) }
