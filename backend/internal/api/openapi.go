package api

import (
	_ "embed"
	"net/http"
)

// OpenAPI is the OpenAPI 3.1 description of this API. It is embedded in the
// binary, served at GET /v1/openapi.json, and kept in sync with the routes by
// TestOpenAPIMatchesRoutes (paths, methods and x-access of every operation).
//
//go:embed openapi.json
var OpenAPI []byte

func serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(OpenAPI)
}
