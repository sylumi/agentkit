// Package skill reads agent skill instructions and resources independently of
// model calls. Skills are immediate subdirectories containing a SKILL.md file.
package skill

import "errors"

var (
	ErrInvalidSkill = errors.New("skill: invalid skill")
	ErrNotFound     = errors.New("skill: not found")
	ErrInvalidPath  = errors.New("skill: invalid path")
	ErrTooLarge     = errors.New("skill: file too large")
)

// Frontmatter contains the YAML header of SKILL.md. AllowedTools is descriptive metadata;
// loading a skill does not grant permissions or register additional tools.
type Frontmatter struct {
	Name          string            `json:"name" yaml:"name"`
	Description   string            `json:"description" yaml:"description"`
	License       string            `json:"license,omitempty" yaml:"license,omitempty"`
	Compatibility string            `json:"compatibility,omitempty" yaml:"compatibility,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	AllowedTools  []string          `json:"allowed-tools,omitempty" yaml:"-"`
}

// Skill contains instructions and resource paths relative to its directory.
// Resource contents are read separately, on demand. Values returned by the
// filesystem reader are independent and may be modified by the caller.
type Skill struct {
	Frontmatter  Frontmatter `json:"frontmatter"`
	Instructions string      `json:"instructions"`
	Resources    []string    `json:"resources"`
}
