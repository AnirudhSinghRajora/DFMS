package openapi

import (
	"bytes"
	"testing"
)

// TestSpecEmbedded verifies that the OpenAPI document was compiled into the
// binary and looks like the expected specification. It guards against the
// embed directive silently picking up an empty or wrong file.
func TestSpecEmbedded(t *testing.T) {
	if len(Spec) == 0 {
		t.Fatal("embedded OpenAPI spec is empty")
	}
	if !bytes.Contains(Spec, []byte("openapi:")) {
		t.Error(`embedded spec is missing the "openapi:" version declaration`)
	}
	if !bytes.Contains(Spec, []byte("paths:")) {
		t.Error(`embedded spec is missing the "paths:" section`)
	}
}
