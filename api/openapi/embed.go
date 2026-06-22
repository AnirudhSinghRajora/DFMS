// Package openapi embeds the DFMS OpenAPI 3.1 specification so services can
// serve it straight from the compiled binary, with no dependency on the spec
// file being present on disk at runtime. This mirrors how the rest of DFMS
// ships as self-contained static binaries (CGO_ENABLED=0) and keeps the API
// documentation in lockstep with the build it describes.
package openapi

import _ "embed"

// Spec is the raw OpenAPI 3.1 specification document, in YAML.
//
//go:embed openapi.yaml
var Spec []byte
