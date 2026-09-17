// Package skill defines skills and their content sources.
package skill

// Frontmatter contains the SKILL.md YAML header. AllowedTools is descriptive only.
type Frontmatter struct {
	Name          string            `json:"name" yaml:"name"`
	Description   string            `json:"description" yaml:"description"`
	License       string            `json:"license,omitempty" yaml:"license,omitempty"`
	Compatibility string            `json:"compatibility,omitempty" yaml:"compatibility,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	AllowedTools  []string          `json:"allowed-tools,omitempty" yaml:"-"`
}

// Skill contains frontmatter, instructions, and relative resource paths.
type Skill struct {
	Frontmatter  Frontmatter `json:"frontmatter"`
	Instructions string      `json:"instructions"`
	Resources    []string    `json:"resources"`
}
