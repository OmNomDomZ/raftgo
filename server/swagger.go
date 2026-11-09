package server

import (
	"embed"
	"net/http"
)

//go:embed swagger/*
var swaggerAssets embed.FS

func swaggerUI(mux *http.ServeMux) {
	mux.HandleFunc("GET /swagger", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swagger/", http.StatusTemporaryRedirect)
	})
	for name, contentType := range map[string]string{
		"index.html":           "text/html; charset=utf-8",
		"init.js":              "text/javascript; charset=utf-8",
		"swagger-ui-bundle.js": "text/javascript; charset=utf-8",
		"swagger-ui.css":       "text/css; charset=utf-8",
		"openapi.json":         "application/json",
	} {
		data, err := swaggerAssets.ReadFile("swagger/" + name)
		if err != nil {
			panic(err)
		}
		path := "/swagger/" + name
		if name == "index.html" {
			path = "/swagger/{$}"
		}
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("X-Content-Type-Options", "nosniff")
			// Swagger UI uses inline styles for operation controls, but scripts and
			// requests remain same-origin. The external specification validator is off.
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
			_, _ = w.Write(data)
		})
	}
}
