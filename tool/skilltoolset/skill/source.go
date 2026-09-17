package skill

import (
	"context"
	"errors"
	"io"
)

// Source errors can be checked with errors.Is.
var (
	ErrInvalidSkill     = errors.New("skill: invalid skill")
	ErrSkillNotFound    = errors.New("skill: skill not found")
	ErrResourceNotFound = errors.New("skill: resource not found")
	ErrDuplicateSkill   = errors.New("skill: duplicate skill")
	ErrInvalidPath      = errors.New("skill: invalid path")
	ErrTooLarge         = errors.New("skill: file too large")
)

// Source reads skill headers, instructions, and resources.
// Implementations return independent values and honor context cancellation.
// Missing skills and resources use ErrSkillNotFound and ErrResourceNotFound, respectively.
type Source interface {
	// ListFrontmatters returns headers sorted by name, reporting invalid or duplicate skills.
	ListFrontmatters(ctx context.Context) ([]Frontmatter, error)

	// LoadFrontmatter returns the YAML header of the named skill.
	LoadFrontmatter(ctx context.Context, name string) (Frontmatter, error)

	// LoadInstructions returns the instruction body of the named skill.
	LoadInstructions(ctx context.Context, name string) (string, error)

	// ListResources returns sorted paths relative to the skill.
	// An empty or "." subpath lists all resources; other subpaths select a directory or file.
	ListResources(ctx context.Context, name, subpath string) ([]string, error)

	// LoadResource opens a raw resource stream. The caller must close it.
	// Paths are relative to the skill, under references/, assets/, or scripts/.
	LoadResource(ctx context.Context, name, resourcePath string) (io.ReadCloser, error)
}
