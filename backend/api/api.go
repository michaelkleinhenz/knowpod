// Package api embeds the OpenAPI description of the HTTP API so the binary can serve it.
package api

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// OpenAPIYAML is openapi.yaml as written.
//
//go:embed openapi.yaml
var OpenAPIYAML []byte

// OpenAPIJSON converts the spec to JSON, for clients that don't read YAML (like the web UI).
func OpenAPIJSON() ([]byte, error) {
	var doc map[string]any
	if err := yaml.Unmarshal(OpenAPIYAML, &doc); err != nil {
		return nil, fmt.Errorf("parse openapi.yaml: %w", err)
	}
	return json.Marshal(doc)
}
