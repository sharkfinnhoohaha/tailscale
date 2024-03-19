// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

package safeweb

import (
	"net/http"
	"testing"

	"github.com/gorilla/csrf"
	"golang.org/x/net/nettest"
)

func TestRequireHostNameOrListener(t *testing.T) {
	_, err := NewServer(Config{})
	if err == nil {
		t.Fatal("expected error when neither Hostname nor Listener is provided")
	}

	_, err = NewServer(Config{Hostname: "localhost"})
	if err != nil {
		t.Fatalf("error creating server with hostname alone: %v", err)
	}

	l, err := nettest.NewLocalListener("tcp")
	if err != nil {
		t.Fatalf("error creating server with listener alone: %v", err)
	}
	defer l.Close()
}

func TestRequireCompleteCrossOriginResourceSharingConfiguration(t *testing.T) {
	_, err := NewServer(Config{Hostname: "foobor", AccessControlAllowOrigin: []string{"https://foobar.com"}})
	if err == nil {
		t.Fatalf("expected error when PermittedCrossOriginHosts is provided without PermittedCrossOriginMethods")
	}

	_, err = NewServer(Config{Hostname: "foobor", AccessControlAllowMethods: []string{"GET", "POST"}})
	if err == nil {
		t.Fatalf("expected error when PermittedCrossOriginMethods is provided without PermittedCrossOriginHosts")
	}

	_, err = NewServer(Config{Hostname: "foobor", AccessControlAllowOrigin: []string{"https://foobar.com"}, AccessControlAllowMethods: []string{"GET", "POST"}})
	if err != nil {
		t.Fatalf("error creating server with complete CORS configuration: %v", err)
	}
}

func TestPostRequestContentTypeValidation(t *testing.T) {
	tests := []struct {
		name         string
		browserRoute bool
		contentType  string
		wantErr      bool
	}{
		{
			name:         "API routes should accept `application/json` content-type",
			browserRoute: false,
			contentType:  "application/json",
			wantErr:      false,
		},
		{
			name:         "API routes should reject `application/x-www-form-urlencoded` content-type",
			browserRoute: false,
			contentType:  "application/x-www-form-urlencoded",
			wantErr:      true,
		},
		{
			name:         "Browser routes should accept `application/x-www-form-urlencoded` content-type",
			browserRoute: true,
			contentType:  "application/x-www-form-urlencoded",
			wantErr:      false,
		},
		{
			name:         "non Browser routes should accept `application/json` content-type",
			browserRoute: true,
			contentType:  "application/json",
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := nettest.NewLocalListener("tcp")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()

			h := &http.ServeMux{}
			h.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("ok"))
			}))
			var s *Server
			if tt.browserRoute {
				s, err = NewServer(Config{BrowserMux: h, Listener: l})
			} else {
				s, err = NewServer(Config{APIMux: h, Listener: l})
			}
			if err != nil {
				t.Fatal(err)
			}
			go s.Serve()

			client := &http.Client{}
			req, err := http.NewRequest("POST", "http://"+l.Addr().String()+"/", nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", tt.contentType)

			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantErr && resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("content type validation failed: got %v; want %v", resp.StatusCode, http.StatusBadRequest)
			}
		})
	}
}

func TestAPIMuxCrossOriginResourceSharingHeaders(t *testing.T) {
	tests := []struct {
		name            string
		httpMethod      string
		wantCORSHeaders bool
		corsOrigins     []string
		corsMethods     []string
	}{
		{
			name:            "do not set CORS headers for non-OPTIONS requests",
			corsOrigins:     []string{"https://foobar.com"},
			corsMethods:     []string{"GET", "POST", "HEAD"},
			httpMethod:      "GET",
			wantCORSHeaders: false,
		},
		{
			name:            "set CORS headers for non-OPTIONS requests",
			corsOrigins:     []string{"https://foobar.com"},
			corsMethods:     []string{"GET", "POST", "HEAD"},
			httpMethod:      "OPTIONS",
			wantCORSHeaders: true,
		},
		{
			name:            "do not serve CORS headers for OPTIONS requests with no configured origins",
			httpMethod:      "OPTIONS",
			wantCORSHeaders: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := nettest.NewLocalListener("tcp")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()

			h := &http.ServeMux{}
			h.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("ok"))
			}))
			s, err := NewServer(Config{
				APIMux:                    h,
				Listener:                  l,
				AccessControlAllowOrigin:  tt.corsOrigins,
				AccessControlAllowMethods: tt.corsMethods,
			})
			if err != nil {
				t.Fatal(err)
			}
			go s.Serve()

			client := &http.Client{}
			req, err := http.NewRequest(tt.httpMethod, "http://"+l.Addr().String()+"/", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}

			if (resp.Header.Get("Access-Control-Allow-Origin") == "") == tt.wantCORSHeaders {
				t.Fatalf("access-control-allow-origin want: %v; got: %v", tt.wantCORSHeaders, resp.Header.Get("Access-Control-Allow-Origin"))
			}
		})
	}
}

func TestBrowserCrossOriginRequestForgeryProtection(t *testing.T) {
	tests := []struct {
		name          string
		apiRoute      bool
		passCSRFToken bool
		wantStatus    int
	}{
		{
			name:          "POST requests to non-API routes require CSRF token and fail if not provided",
			apiRoute:      false,
			passCSRFToken: false,
			wantStatus:    http.StatusForbidden,
		},
		{
			name:          "POST requests to non-API routes require CSRF token and pass if provided",
			apiRoute:      false,
			passCSRFToken: true,
			wantStatus:    http.StatusOK,
		},
		{
			name:          "POST requests to /api/ routes do not require CSRF token",
			apiRoute:      true,
			passCSRFToken: false,
			wantStatus:    http.StatusOK,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := nettest.NewLocalListener("tcp")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()

			h := &http.ServeMux{}
			h.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("ok"))
			}))
			var s *Server
			if tt.apiRoute {
				s, err = NewServer(Config{APIMux: h, Listener: l})
			} else {
				s, err = NewServer(Config{BrowserMux: h, Listener: l})
			}
			if err != nil {
				t.Fatal(err)
			}
			go s.Serve()

			client := &http.Client{Jar: http.CookieJar(nil)}
			target := "http://" + l.Addr().String()

			// construct the test request
			req, err := http.NewRequest("POST", target+"/", nil)
			if err != nil {
				t.Fatal(err)
			}

			// send JSON for API routes, form data for browser routes
			if tt.apiRoute {
				req.Header.Set("Content-Type", "application/json")
			} else {
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			}

			// retrieve CSRF cookie & pass it in the test request
			// ref: https://github.com/gorilla/csrf/blob/main/csrf_test.go#L344-L347
			var token string
			if tt.passCSRFToken {
				h.Handle("/csrf", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
					token = csrf.Token(r)
				}))
				get, err := http.NewRequest("GET", target+"/csrf", nil)
				if err != nil {
					t.Fatal(err)
				}
				resp, err := client.Do(get)
				if err != nil {
					t.Fatal(err)
				}

				// pass the token & cookie in our subsequent test request
				req.Header.Set("X-CSRF-Token", token)
				for _, c := range resp.Cookies() {
					req.AddCookie(c)
				}
			}

			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}

			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("csrf protection check failed: got %v; want %v", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestContentSecurityPolicyHeader(t *testing.T) {
	tests := []struct {
		name     string
		apiRoute bool
		wantCSP  bool
	}{
		{
			name:     "default routes get CSP headers",
			apiRoute: false,
			wantCSP:  true,
		},
		{
			name:     "`/api/*` routes do not get CSP headers",
			apiRoute: true,
			wantCSP:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := nettest.NewLocalListener("tcp")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()

			h := &http.ServeMux{}
			h.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte("ok"))
			}))
			var s *Server
			if tt.apiRoute {
				s, err = NewServer(Config{APIMux: h, Listener: l})
			} else {
				s, err = NewServer(Config{BrowserMux: h, Listener: l})
			}
			if err != nil {
				t.Fatal(err)
			}
			go s.Serve()

			resp, err := http.Get("http://" + l.Addr().String() + "/")
			if err != nil {
				t.Fatal(err)
			}

			if (resp.Header.Get("Content-Security-Policy") == "") == tt.wantCSP {
				t.Fatalf("content security policy want: %v; got: %v", tt.wantCSP, resp.Header.Get("Content-Security-Policy"))
			}
		})
	}
}
