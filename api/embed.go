// Package api embeds the OpenAPI specification of the service.
package api

import _ "embed"

// OpenAPISpec is the OpenAPI 3 document describing the HTTP API.
//
//go:embed openapi.yaml
var OpenAPISpec []byte
