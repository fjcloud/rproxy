package proxy

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
)

// NewProxyHandler creates the main HTTP handler.
func NewProxyHandler(router *Router) http.Handler {
	director := func(req *http.Request) {
		fqdn := req.Host // Use the Host header (which includes port if specified)
		// If Host includes port, strip it for lookup
		host, _, err := net.SplitHostPort(fqdn)
		if err == nil {
			fqdn = host
		}

		route, exists := router.GetRoute(fqdn)
		if !exists {
			slog.Warn("Handler: No route found", "fqdn", fqdn)
			// Set a special header or context value to indicate no route found
			// The error handler will then pick this up.
			req.Header.Set("X-RProxy-Error", "No route found")
			// Set a dummy scheme and host to prevent httputil panicking
			req.URL.Scheme = "http"
			req.URL.Host = "invalid-internal-host"
			return
		}

		targetURL := &url.URL{
			Scheme: "http", // Assuming backend is always HTTP for now
			Host:   net.JoinHostPort(route.TargetIP, fmt.Sprintf("%d", route.TargetPort)),
		}

		req.URL.Scheme = targetURL.Scheme
		req.URL.Host = targetURL.Host
		req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
		req.Header.Set("X-Forwarded-Proto", "https") // We are terminating TLS
		req.Host = targetURL.Host // Set Host header to the target's host

		// DEBUG level logging can be achieved by setting the slog level in main.go
		// slog.Debug("Handler: Proxying request", "fqdn", fqdn, "target", targetURL.Host, "path", req.URL.Path)
		// log.Printf("[DEBUG] Handler: Proxying %s -> %s%s", fqdn, targetURL.Host, req.URL.Path)
	}

	errorHandler := func(rw http.ResponseWriter, req *http.Request, err error) {
		if req.Header.Get("X-RProxy-Error") == "No route found" {
			slog.Warn("Handler: Responding 502 Bad Gateway (No route found)", "host", req.Host)
			rw.WriteHeader(http.StatusBadGateway)
			fmt.Fprintln(rw, "502 Bad Gateway: No backend service available for this host.")
			return
		}

		// Default error handling for other proxy errors (e.g., connection refused)
		slog.Error("Handler: Proxy error", "host", req.Host, "error", err)
		rw.WriteHeader(http.StatusBadGateway) // 502 usually appropriate for backend errors
		fmt.Fprintf(rw, "502 Bad Gateway: %v", err)
	}

	proxy := &httputil.ReverseProxy{
		Director:     director,
		ErrorHandler: errorHandler,
		// ModifyResponse can be added later if needed
		// BufferPool can be added later for performance
	}

	return proxy
} 