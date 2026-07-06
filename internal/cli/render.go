package cli

import (
	"encoding/json"
	"io"

	"gopkg.in/yaml.v3"
)

// Supported values for the global --output flag.
const (
	outputTable = "table"
	outputJSON  = "json"
	outputYAML  = "yaml"
)

// writeStructured renders v in the requested machine-readable format and reports
// whether it handled the format. For the human-oriented "table" format (or any
// value it does not recognize) it returns (false, nil) so the caller can emit
// its own layout. This keeps each command's table rendering local while sharing
// the JSON/YAML encoding in one place.
func writeStructured(w io.Writer, format string, v any) (handled bool, err error) {
	switch format {
	case outputJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return true, enc.Encode(v)
	case outputYAML:
		enc := yaml.NewEncoder(w)
		enc.SetIndent(2)
		err = enc.Encode(v)
		if cerr := enc.Close(); err == nil {
			err = cerr
		}
		return true, err
	default:
		return false, nil
	}
}
