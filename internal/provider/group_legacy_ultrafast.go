package provider

import (
	"bytes"
	"encoding/json"
)

// decodeProviderFile carries the fork's saved group speed into upstream's
// ultra field. Ordinary files use the original decoder; only an old file
// needs another look. Group itself is also embedded in API requests, where
// its own UnmarshalJSON would hide the surrounding request's fields.
func decodeProviderFile(data []byte, f *file) error {
	if err := json.Unmarshal(data, f); err != nil {
		return err
	}
	if !bytes.Contains(data, []byte(`"ultrafast"`)) {
		return nil
	}
	var old struct {
		Groups []struct {
			Ultrafast []string `json:"ultrafast"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(data, &old); err != nil {
		return err
	}
	for i, saved := range old.Groups {
		g := &f.Groups[i]
		if g.Ultra == nil && saved.Ultrafast != nil {
			g.Ultra = saved.Ultrafast
			g.Fast = compact(append(g.Fast, g.Ultra...))
		}
	}
	return nil
}
