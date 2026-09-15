// sync-models downloads all models.dev data and replaces the embedded catalog.
package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/sylumi/agentkit/internal/modeldata"
)

const (
	maxSourceBytes = 32 << 20
	outputPath     = "modelcatalog/data/models.json"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	// Download the full models.dev JSON.
	data, err := fetch()
	if err != nil {
		return err
	}
	// Convert to ModelInfo values in a sorted snapshot.
	snapshot, err := modeldata.DecodeModelsDev(bytes.NewReader(data), time.Now().UTC())
	if err != nil {
		return err
	}
	// Save the complete snapshot to the local models.json file.
	if err := saveSnapshot(snapshot); err != nil {
		return err
	}
	_, err = fmt.Printf("Wrote %d models to %s\n", len(snapshot.Models), outputPath)
	return err
}

func fetch() ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	r, err := client.Get(modeldata.ModelsDevURL)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev: HTTP %d", r.StatusCode)
	}
	// Read one extra byte to detect responses over the size limit.
	data, err := io.ReadAll(io.LimitReader(r.Body, maxSourceBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSourceBytes {
		return nil, fmt.Errorf("models.dev: response exceeds %d bytes", maxSourceBytes)
	}
	return data, nil
}

// Write the complete snapshot before replacing the existing file.
func saveSnapshot(snapshot *modeldata.Snapshot) error {
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Use the same directory so Rename can replace the target atomically.
	f, err := os.CreateTemp(dir, ".models-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(0644); err != nil {
		return err
	}
	// Encode the snapshot as JSON in the temporary file.
	if err := modeldata.Encode(f, snapshot); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), outputPath)
}
