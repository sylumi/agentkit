// Package modeldata implements the private catalog file format and source
// conversion. Public catalog queries live in modelcatalog.
package modeldata

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/sylumi/agentkit/model"
)

const Version = 2
const ModelsDevURL = "https://models.dev/api.json"

type Snapshot struct {
	Version   int               `json:"version"`
	Source    string            `json:"source"`
	FetchedAt time.Time         `json:"fetched_at"`
	Models    []model.ModelInfo `json:"models"`
}

func Decode(r io.Reader) (*Snapshot, error) {
	var s Snapshot
	if err := decodeJSON(r, &s); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	s.Sort()
	return &s, nil
}

// Encode writes a snapshot as JSON.
func Encode(w io.Writer, s *Snapshot) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func decodeJSON(r io.Reader, v any) error {
	d := json.NewDecoder(r)
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(json.RawMessage)); err != io.EOF {
		return fmt.Errorf("expected one JSON value")
	}
	return nil
}

func (s *Snapshot) Sort() {
	slices.SortFunc(s.Models, func(a, b model.ModelInfo) int {
		if c := strings.Compare(a.Provider.ID, b.Provider.ID); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
}

func validID(id string) error {
	if id == "" || strings.TrimSpace(id) != id {
		return fmt.Errorf("required without surrounding whitespace")
	}
	return nil
}
