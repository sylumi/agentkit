package modelcatalog

import (
	"bytes"
	_ "embed"
	"fmt"
	"sync"

	"github.com/sylumi/agentkit/model"
)

//go:embed data/models.json
var builtin []byte

var loadBuiltin = sync.OnceValues(func() (*Catalog, error) {
	return Load(bytes.NewReader(builtin))
})

// Builtin loads the embedded snapshot on first use and caches the catalog or
// loading error. Concurrent callers share the same immutable catalog. Updates
// require an explicit sync and rebuild; loading never downloads data.
func Builtin() (*Catalog, error) { return loadBuiltin() }

// Lookup finds a model in the built-in catalog and returns an independent copy.
// It returns an error if loading fails or the model does not exist.
func Lookup(providerID, modelID string) (model.ModelInfo, error) {
	catalog, err := Builtin()
	if err != nil {
		return model.ModelInfo{}, err
	}
	info, ok := catalog.Lookup(providerID, modelID)
	if !ok {
		return model.ModelInfo{}, fmt.Errorf(
			"modelcatalog: model %q/%q not found",
			providerID,
			modelID,
		)
	}
	return info, nil
}

// MustLookup returns model metadata, panicking if Lookup fails.
// Use it for fixed model IDs in application configuration.
func MustLookup(providerID, modelID string) model.ModelInfo {
	info, err := Lookup(providerID, modelID)
	if err != nil {
		panic(err)
	}
	return info
}

// List returns independent copies from the built-in catalog, ordered by provider
// ID and model ID. An empty providerID selects all providers; no matches returns
// an empty list. A loading failure is returned as an error.
func List(providerID string) ([]model.ModelInfo, error) {
	catalog, err := Builtin()
	if err != nil {
		return nil, err
	}
	return catalog.List(providerID), nil
}
