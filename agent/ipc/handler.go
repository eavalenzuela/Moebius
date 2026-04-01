package ipc

import (
	"context"
	"encoding/json"
	"sync"
)

// HandlerFunc processes an IPC request and returns a result to be JSON-marshalled,
// or an error. The context carries the connection lifetime.
type HandlerFunc func(ctx context.Context, params json.RawMessage) (any, error)

// Router maps method names to handler functions.
type Router struct {
	mu       sync.RWMutex
	handlers map[string]HandlerFunc
}

// NewRouter creates an empty Router.
func NewRouter() *Router {
	return &Router{handlers: make(map[string]HandlerFunc)}
}

// Handle registers a handler for the given method name.
func (r *Router) Handle(method string, h HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[method] = h
}

type tokenKey struct{}

// TokenFromContext returns the session token from the context, if present.
func TokenFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(tokenKey{}).(string); ok {
		return v
	}
	return ""
}

// Dispatch finds and invokes the handler for req.Method.
// If req.Token is set, it is stored in the context for auth middleware.
// Returns a Response ready to send back to the client.
func (r *Router) Dispatch(ctx context.Context, req *Request) *Response {
	r.mu.RLock()
	h, ok := r.handlers[req.Method]
	r.mu.RUnlock()

	if !ok {
		return &Response{
			ID: req.ID,
			Error: &Error{
				Code:    CodeMethodNotFound,
				Message: "method not found: " + req.Method,
			},
		}
	}

	if req.Token != "" {
		ctx = context.WithValue(ctx, tokenKey{}, req.Token)
	}

	result, err := h(ctx, req.Params)
	if err != nil {
		return errorResponse(req.ID, err)
	}

	data, err := json.Marshal(result)
	if err != nil {
		return &Response{
			ID: req.ID,
			Error: &Error{
				Code:    CodeInternal,
				Message: "marshal result: " + err.Error(),
			},
		}
	}

	return &Response{ID: req.ID, Result: data}
}

// errorResponse converts an error to a Response. If the error is an *Error,
// its code is preserved; otherwise CodeInternal is used.
func errorResponse(id string, err error) *Response {
	if e, ok := err.(*Error); ok { //nolint:errorlint // intentional type check
		return &Response{ID: id, Error: e}
	}
	return &Response{
		ID: id,
		Error: &Error{
			Code:    CodeInternal,
			Message: err.Error(),
		},
	}
}
