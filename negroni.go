package negroni

import (
	"log"
	"net/http"
	"os"
)

const (
	// DefaultAddress is used if no other is specified.
	DefaultAddress = ":8080" // 默认路由地址
)

// Handler handler is an interface that objects can implement to be registered to serve as middleware
// in the Negroni middleware stack.
// ServeHTTP should yield to the next middleware in the chain by invoking the next http.HandlerFunc
// passed in.
//
// If the Handler writes to the ResponseWriter, the next http.HandlerFunc should not be invoked.
// Handler 接口
type Handler interface {
	ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc)
}

// HandlerFunc is an adapter to allow the use of ordinary functions as Negroni handlers.
// If f is a function with the appropriate signature, HandlerFunc(f) is a Handler object that calls f.
// HandlerFunc 和Handler要求实现的ServeHTTP函数签名一致
type HandlerFunc func(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc)

// HandlerFunc 也实现了Handler接口本身，就是调用自己
func (h HandlerFunc) ServeHTTP(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	h(rw, r, next)
}

// middleware 实现了Handler
type middleware struct {
	handler Handler

	// nextfn stores the next.ServeHTTP to reduce memory allocate
	// 这里不是存储middleware 而是存储了 middleware.handler.ServeHTTP
	nextfn func(rw http.ResponseWriter, r *http.Request)
}

func newMiddleware(handler Handler, next *middleware) middleware {
	// 把一个handler和一个middleware生成一个新的middleware
	return middleware{
		handler: handler,
		nextfn:  next.ServeHTTP, // 下一个middleware的ServeHTTP
	}
}

// middleware的ServeHTTP方法是调用当前middleware中handler的ServeHTTP方法
func (m middleware) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	// 具体的调用时机是handler.ServeHTTP 中调用next(rw, r)的时候
	// 执行这个middleware的handler的ServeHTTP，并把下一个middleware需要执行的ServeHTTP传入
	m.handler.ServeHTTP(rw, r, m.nextfn)
}

// Wrap converts a http.Handler into a negroni.Handler so it can be used as a Negroni
// middleware. The next http.HandlerFunc is automatically called after the Handler
// is executed.
// 把http.Handler封装成negroni.Handler也可以当成middleware因为会调用下一个HandlerFunc
func Wrap(handler http.Handler) Handler {
	return HandlerFunc(func(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		handler.ServeHTTP(rw, r)
		next(rw, r)
	})
}

// WrapFunc converts a http.HandlerFunc into a negroni.Handler so it can be used as a Negroni
// middleware. The next http.HandlerFunc is automatically called after the Handler
// is executed.
// 类似于Wrap
func WrapFunc(handlerFunc http.HandlerFunc) Handler {
	return HandlerFunc(func(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
		handlerFunc(rw, r)
		next(rw, r)
	})
}

// Negroni is a stack of Middleware Handlers that can be invoked as an http.Handler.
// Negroni middleware is evaluated in the order that they are added to the stack using
// the Use and UseHandler methods.
type Negroni struct {
	middleware middleware // 头middleware
	handlers   []Handler  // 所有middleware的handler，方便在有新的handler加入时，重建middleware链
}

// New returns a new Negroni instance with no middleware preconfigured.
func New(handlers ...Handler) *Negroni {
	return &Negroni{
		handlers:   handlers,
		middleware: build(handlers),
	}
}

// With returns a new Negroni instance that is a combination of the negroni
// receiver's handlers and the provided handlers.
// 加入新的Handlers并重建middleware返回新的Negroni对象
func (n *Negroni) With(handlers ...Handler) *Negroni {
	currentHandlers := make([]Handler, len(n.handlers))
	copy(currentHandlers, n.handlers)
	return New(
		append(currentHandlers, handlers...)...,
	)
}

// Classic returns a new Negroni instance with the default middleware already
// in the stack.
//
// Recovery - Panic Recovery Middleware
// Logger - Request/Response Logging
// Static - Static File Serving
// 使用默认的middleware
func Classic() *Negroni {
	return New(NewRecovery(), NewLogger(), NewStatic(http.Dir("public")))
}

// 实现http.Handler
func (n *Negroni) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	n.middleware.ServeHTTP(NewResponseWriter(rw), r)
}

// Use adds a Handler onto the middleware stack. Handlers are invoked in the order they are added to a Negroni.
func (n *Negroni) Use(handler Handler) {
	if handler == nil {
		panic("handler cannot be nil")
	}

	n.handlers = append(n.handlers, handler)
	n.middleware = build(n.handlers) // 重新建立middleware
}

// UseFunc adds a Negroni-style handler function onto the middleware stack.
func (n *Negroni) UseFunc(handlerFunc func(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc)) {
	n.Use(HandlerFunc(handlerFunc))
}

// UseHandler adds a http.Handler onto the middleware stack. Handlers are invoked in the order they are added to a Negroni.
func (n *Negroni) UseHandler(handler http.Handler) {
	n.Use(Wrap(handler))
}

// UseHandlerFunc adds a http.HandlerFunc-style handler function onto the middleware stack.
func (n *Negroni) UseHandlerFunc(handlerFunc func(rw http.ResponseWriter, r *http.Request)) {
	n.UseHandler(http.HandlerFunc(handlerFunc))
}

// Run is a convenience function that runs the negroni stack as an HTTP
// server. The addr string, if provided, takes the same format as http.ListenAndServe.
// If no address is provided but the PORT environment variable is set, the PORT value is used.
// If neither is provided, the address' value will equal the DefaultAddress constant.
func (n *Negroni) Run(addr ...string) {
	l := log.New(os.Stdout, "[negroni] ", 0)
	finalAddr := detectAddress(addr...)
	l.Printf("listening on %s", finalAddr)
	l.Fatal(http.ListenAndServe(finalAddr, n))
}

func detectAddress(addr ...string) string {
	if len(addr) > 0 {
		return addr[0]
	}
	if port := os.Getenv("PORT"); port != "" {
		return ":" + port
	}
	return DefaultAddress
}

// Returns a list of all the handlers in the current Negroni middleware chain.
func (n *Negroni) Handlers() []Handler {
	return n.handlers
}

func build(handlers []Handler) middleware {
	var next middleware
	// 最终形成的链条 middleware1 -> middleware2 -> middleware3 -> voidMiddleware
	switch {
	case len(handlers) == 0: // 传入的handlers为空不会进入递归，也不会由递归进入
		return voidMiddleware()
	case len(handlers) > 1: // 递归，直到len(handlers) == 1
		next = build(handlers[1:])
	default: // len(handlers) == 1 的情况直接把当前唯一handler和空Middleware合成新的Middleware
		next = voidMiddleware()
	}
	return newMiddleware(handlers[0], &next)
}

func voidMiddleware() middleware { // 空的中间件
	return newMiddleware(
		HandlerFunc(func(rw http.ResponseWriter, r *http.Request, next http.HandlerFunc) {}),
		&middleware{},
	)
}
