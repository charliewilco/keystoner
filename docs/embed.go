// Package docs embeds the public API contract in the server binary.
package docs

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte
