package api

import (
	"context"
	"net/http"
	"strings"
)

// trieNode is a node in the trie-based router.
type trieNode struct {
	children map[string]*trieNode
	param    *trieNode
	paramKey string
	handlers map[string]http.HandlerFunc
}

func newTrieNode() *trieNode {
	return &trieNode{
		children: make(map[string]*trieNode),
		handlers: make(map[string]http.HandlerFunc),
	}
}

// Router is a custom trie-based HTTP router with path parameter support.
type Router struct {
	root        *trieNode
	middlewares []Middleware
}

// Middleware wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// NewRouter creates a new router.
func NewRouter() *Router {
	return &Router{
		root: newTrieNode(),
	}
}

// Use adds a middleware to the router.
func (r *Router) Use(mw Middleware) {
	r.middlewares = append(r.middlewares, mw)
}

// Handle registers a handler for a method and path pattern.
// Path parameters are specified as {name}.
func (r *Router) Handle(method, pattern string, handler http.HandlerFunc) {
	parts := splitPath(pattern)
	node := r.root

	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			paramKey := part[1 : len(part)-1]
			if node.param == nil {
				node.param = newTrieNode()
			}
			node.param.paramKey = paramKey
			node = node.param
		} else {
			if child, ok := node.children[part]; ok {
				node = child
			} else {
				child := newTrieNode()
				node.children[part] = child
				node = child
			}
		}
	}

	node.handlers[method] = handler
}

// GET registers a GET handler.
func (r *Router) GET(pattern string, handler http.HandlerFunc) {
	r.Handle(http.MethodGet, pattern, handler)
}

// POST registers a POST handler.
func (r *Router) POST(pattern string, handler http.HandlerFunc) {
	r.Handle(http.MethodPost, pattern, handler)
}

// PUT registers a PUT handler.
func (r *Router) PUT(pattern string, handler http.HandlerFunc) {
	r.Handle(http.MethodPut, pattern, handler)
}

// DELETE registers a DELETE handler.
func (r *Router) DELETE(pattern string, handler http.HandlerFunc) {
	r.Handle(http.MethodDelete, pattern, handler)
}

// ServeHTTP implements the http.Handler interface.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	var handler http.Handler = http.HandlerFunc(r.dispatch)
	for i := len(r.middlewares) - 1; i >= 0; i-- {
		handler = r.middlewares[i](handler)
	}
	handler.ServeHTTP(w, req)
}

func (r *Router) dispatch(w http.ResponseWriter, req *http.Request) {
	parts := splitPath(req.URL.Path)
	params := make(map[string]string)

	node := r.root
	for _, part := range parts {
		if child, ok := node.children[part]; ok {
			node = child
		} else if node.param != nil {
			params[node.param.paramKey] = part
			node = node.param
		} else {
			http.NotFound(w, req)
			return
		}
	}

	handler, ok := node.handlers[req.Method]
	if !ok {
		if len(node.handlers) > 0 {
			w.Header().Set("Allow", allowedMethods(node.handlers))
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		} else {
			http.NotFound(w, req)
		}
		return
	}

	// Inject params into context
	ctx := req.Context()
	for k, v := range params {
		ctx = context.WithValue(ctx, paramKey(k), v)
	}
	handler.ServeHTTP(w, req.WithContext(ctx))
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func allowedMethods(handlers map[string]http.HandlerFunc) string {
	methods := make([]string, 0, len(handlers))
	for m := range handlers {
		methods = append(methods, m)
	}
	return strings.Join(methods, ", ")
}

type paramKey string

// Param retrieves a route parameter from the request.
func Param(r *http.Request, key string) string {
	val := r.Context().Value(paramKey(key))
	if val == nil {
		return ""
	}
	return val.(string)
}
