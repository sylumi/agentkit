// Package modelcatalog provides offline model metadata. Loading and querying a
// catalog never reads credentials or contacts a model service.
package modelcatalog

import (
	"io"

	"github.com/sylumi/agentkit/internal/modeldata"
	"github.com/sylumi/agentkit/model"
)

// Catalog is immutable after loading and supports concurrent queries. Returned
// descriptions are independent copies, including nested slices and pointers.
type Catalog struct {
	models map[modelKey]model.ModelInfo
	keys   []modelKey
}

type modelKey struct{ provider, model string }

// Load decodes one catalog snapshot, without network access or metadata validation.
func Load(r io.Reader) (*Catalog, error) {
	s, err := modeldata.Decode(r)
	if err != nil {
		return nil, err
	}
	c := &Catalog{models: make(map[modelKey]model.ModelInfo)}
	for _, m := range s.Models {
		key := modelKey{m.Provider.ID, m.ID}
		c.models[key] = m
		c.keys = append(c.keys, key)
	}
	return c, nil
}

// Lookup finds a model by its provider and model IDs.
func (c *Catalog) Lookup(providerID, modelID string) (model.ModelInfo, bool) {
	m, ok := c.models[modelKey{providerID, modelID}]
	if !ok {
		return model.ModelInfo{}, false
	}
	return cloneModelInfo(m), true
}

// List returns models ordered by provider ID and model ID. An empty providerID
// selects all providers. Catalog inclusion does not imply adapter support.
func (c *Catalog) List(providerID string) []model.ModelInfo {
	var result []model.ModelInfo
	for _, key := range c.keys {
		if providerID == "" || key.provider == providerID {
			result = append(result, cloneModelInfo(c.models[key]))
		}
	}
	return result
}
