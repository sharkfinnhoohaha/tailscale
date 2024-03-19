// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

// Package safeweb provides a simple, safe, and opinionated wrapper around
// http.Server that applies some web application security defenses by default.
// It is intended to be used in place of http.Server internal applications that
// reside on the Tailscale network.
//
// safeweb requires that applications be built using two distinct
// `http.ServeMux`s for serving browser and API clients to serve the appropriate
// security defenses in their respective contexts. When serving requests it
// will first attempt to route requests to the browser mux, and if no matching
// route is found it will instead use the API mux.
//
// safeweb enforces Cross-Site Request Forgery (CSRF) protection for all routes
// in the browser mux using the gorilla/csrf package. It is necessary to
// template the CSRF token into all forms that are submitted to the server using
// the `csrf.TemplateField` and `csrf.TemplateTag(r *http.Request)` APIs.
//
// safeweb expects either a Hostname or a Listener to be provided in the
// configuration. If a hostname is provided it will create a tsnet server by
// that name and serve the application over HTTPS. It will also create a HTTP to
// HTTPS redirect for the same host. If a listener is provided safeweb will
// serve the application over HTTP over that listener alone. It is the caller's
// responsibility to ensure that the listener is closed.
//
// safeweb will apply the following to browser requests:
//   - A Content-Security-Policy header that disallows inline scripts, framing, and third party resources.
//   - Cross-Site Request Forgery protection for all forms.
//   - X-Content-Type-Options header set to "nosniff" to prevent MIME type sniffing attacks.
//
// safeweb will apply the following to API requests:
//   - Content-Type header validation to disallow `application/x-www-form-urlencoded` requests.
//   - Cross-Origin Resource Sharing headers if they are provided in the configuration.
//
// example usage:
//
//	h := http.NewServeMux()
//	h.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
//		fmt.Fprint(w, "Hello, world!")
//	})
//	s, err := safeweb.NewServer(safeweb.Config{
//		Hostname:   "my-service",
//		BrowserMux: h,
//	})
//	if err != nil {
//		log.Fatalf("failed to create server: %v", err)
//	}
//	if err := s.Serve(); err != nil && err != http.ErrServerClosed {
//		log.Fatalf("failed to serve: %v", err)
//	}
package safeweb

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/csrf"
	"tailscale.com/tsnet"
)

// the default Content-Security-Policy header.
var defaultCSP = strings.Join([]string{
	`default-src 'self'`,      // origin is the only valid source for all content types
	`script-src 'self'`,       // disallow inline javascript
	`frame-ancestors 'none'`,  // disallow framing of the page
	`form-action 'self'`,      // disallow form submissions to other origins
	`base-uri 'self'`,         // disallow base URIs from other origins
	`block-all-mixed-content`, // disallow mixed content when serving over HTTPS
	`object-src 'none'`,       // disallow embedding of resources from other origins
}, "; ")

// Config contains the configuration for a safeweb server.
type Config struct {
	// Hostname is the name of the tsnet service that will be created to host
	// the application.
	Hostname string

	// Listener is where the server will listen for client connections.
	// Providing a listening will override the use of tsnet and the Hostname field.
	Listener net.Listener

	// BrowserMux is the HTTP handler for any routes in your application that
	// should only be served to browsers in a primary origin context. These
	// requests will be subject to CSRF protection and will have
	// browser-specific headers in their responses.
	BrowserMux *http.ServeMux

	// APIMux is the HTTP handler for any routes in your application that
	// should only be served to non-browser clients or to browsers in a
	// cross-origin resource sharing context.
	APIMux *http.ServeMux

	// AccessControlAllowOrigin specifies the Access-Control-Allow-Origin header sent in response to pre-flight OPTIONS requests.
	// Expects a list of origins, e.g. ["https://foobar.com", "https://foobar.net"] or the wildcard value ["*"].
	// No headers will be sent if no origins are provided.
	AccessControlAllowOrigin []string
	// AccessControlAllowMethods specifies the Access-Control-Allow-Methods header sent in response to pre-flight OPTIONS requests.
	// Expects a list of methods, e.g. ["GET", "POST", "PUT", "DELETE"].
	// No headers will be sent if no methods are provided.
	AccessControlAllowMethods []string

	// CSRFSecret is the secret used to sign CSRF tokens. It must be 32 bytes long.
	// This should be considered a sensitive value and should be kept secret.
	// TODO(@patrickod) do we want to keep this as a toggle? the intent is to
	// prevent CSRF failures that occur due to the secret rotating between
	// server restarts.
	CSRFSecret []byte
}

func (c *Config) setDefaults() error {
	if c.BrowserMux == nil {
		c.BrowserMux = &http.ServeMux{}
	}

	if c.APIMux == nil {
		c.APIMux = &http.ServeMux{}
	}

	if c.CSRFSecret == nil || len(c.CSRFSecret) == 0 {
		c.CSRFSecret = make([]byte, 32)
		if _, err := crand.Read(c.CSRFSecret); err != nil {
			return fmt.Errorf("failed to generate CSRF secret: %w", err)
		}
	}

	return nil
}

func (c *Config) handler() http.Handler {
	// only serve HTTPS over tsnet when a listener is not provided.
	serveHTTPS := c.Listener == nil
	csrfProtect := csrf.Protect(c.CSRFSecret, csrf.Secure(serveHTTPS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// determine which of the two muxes should serve the request
		var apiRequest bool
		if _, p := c.BrowserMux.Handler(r); p == "" {
			apiRequest = true
		}

		if apiRequest {
			// disallow x-www-form-urlencoded requests to the API
			if r.Header.Get("Content-Type") == "application/x-www-form-urlencoded" {
				http.Error(w, "invalid content type", http.StatusBadRequest)
				return
			}

			// set CORS headers for pre-flight OPTIONS requests if any were configured
			if r.Method == "OPTIONS" && len(c.AccessControlAllowOrigin) > 0 {
				w.Header().Set("Access-Control-Allow-Origin", strings.Join(c.AccessControlAllowOrigin, ", "))
				w.Header().Set("Access-Control-Allow-Methods", strings.Join(c.AccessControlAllowMethods, ", "))
			}
			c.APIMux.ServeHTTP(w, r)
		} else {
			// TODO(@patrickod) consider templating additions to the CSP header.
			w.Header().Set("Content-Security-Policy", defaultCSP)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			csrfProtect(c.BrowserMux).ServeHTTP(w, r)
		}
	})
}

// Server is a safeweb server.
type Server struct {
	Config
	h *http.Server
}

// NewServer creates a safeweb server with the provided configuration. It will
// validate the configuration to ensure that it is complete and return an error
// if not.
func NewServer(config Config) (*Server, error) {
	// ensure we have a valid listener configuration
	if config.Hostname == "" && config.Listener == nil {
		return nil, fmt.Errorf("must provide one of either Hostname or Listener")
	}

	// ensure that CORS configuration is complete
	providedCORSMethods := len(config.AccessControlAllowMethods) > 0
	providedCORSHosts := len(config.AccessControlAllowOrigin) > 0
	if providedCORSMethods != providedCORSHosts {
		return nil, fmt.Errorf("must provide both PermittedCrossOriginMethods and PermittedCrossOriginHosts")
	}

	// fill in any missing fields
	if err := config.setDefaults(); err != nil {
		return nil, fmt.Errorf("failed to set defaults: %w", err)
	}

	return &Server{
		config,
		&http.Server{Handler: config.handler()},
	}, nil
}

func (s *Server) redirectHTTP(fqdn string) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		new := url.URL{
			Scheme:   "https",
			Host:     fqdn,
			Path:     r.URL.Path,
			RawQuery: r.URL.RawQuery,
		}

		http.Redirect(w, r, new.String(), http.StatusMovedPermanently)
	}
}

// Serve creates listeners and begins serving the application. It will block
// until the server is shut down.
func (s *Server) Serve() error {
	// serve only HTTP if a listener is provided.
	if s.Listener != nil {
		return s.h.Serve(s.Listener)
	}

	// if a hostname is provided, create a tsnet server by that name and serve
	// both HTTP and HTTPS traffic.
	ts := tsnet.Server{
		Hostname: s.Hostname,
	}

	// await a successful tsnet server up status to understand our FQDN for HTTP
	// redirects.
	var fqdn string
	status, err := ts.Up(context.Background())
	if err == nil && status != nil {
		fqdn = strings.TrimSuffix(status.Self.DNSName, ".")
	}

	// serve HTTP redirect to HTTPS
	http80, err := ts.Listen("tcp", ":80")
	if err != nil {
		return fmt.Errorf("failed to listen on port 80: %w", err)
	}
	go func() {
		if err := http.Serve(http80, http.HandlerFunc(s.redirectHTTP(fqdn))); err != nil && err != http.ErrServerClosed {
			log.Println(err)
		}
	}()

	// serve HTTPS
	http443, err := ts.ListenTLS("tcp", ":443")
	if err != nil {
		return fmt.Errorf("failed to listen on port 443: %w", err)
	}
	return s.h.Serve(http443)
}
