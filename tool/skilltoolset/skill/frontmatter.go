package skill

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

const maxSkillBytes = 256 << 10

// Parse reads one SKILL.md document, preserving the Markdown body verbatim.
// Resources are populated by FileSystem.Load, not by parsing. Both LF and CRLF
// delimiters are accepted. Documents larger than 256 KiB are rejected.
func Parse(data []byte) (Skill, error) {
	if len(data) > maxSkillBytes {
		return Skill{}, fmt.Errorf("%w: SKILL.md exceeds %d bytes", ErrTooLarge, maxSkillBytes)
	}
	if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return Skill{}, fmt.Errorf("%w: SKILL.md must be UTF-8 text", ErrInvalidSkill)
	}
	r := bufio.NewReader(bytes.NewReader(data))
	metadata, err := parseFrontmatter(r)
	if err != nil {
		return Skill{}, err
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return Skill{}, err
	}
	return Skill{Metadata: metadata, Instructions: string(body), Resources: []string{}}, nil
}

func parseFrontmatter(r *bufio.Reader) (Metadata, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return Metadata{}, fmt.Errorf("read frontmatter: %w", err)
	}
	if err != nil || strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r") != "---" {
		return Metadata{}, fmt.Errorf("%w: expected opening frontmatter delimiter", ErrInvalidSkill)
	}
	var header strings.Builder
	for {
		line, err = r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return Metadata{}, fmt.Errorf("read frontmatter: %w", err)
		}
		if strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r") == "---" {
			break
		}
		if err != nil {
			return Metadata{}, fmt.Errorf("%w: missing closing frontmatter delimiter", ErrInvalidSkill)
		}
		header.WriteString(line)
	}
	// Use a node for allowed-tools: the specification uses a scalar, while some
	// skill authors use a YAML sequence. Unknown frontmatter fields are ignored.
	var wire struct {
		Name          yamlText            `yaml:"name"`
		Description   yamlText            `yaml:"description"`
		License       yamlText            `yaml:"license"`
		Compatibility yamlText            `yaml:"compatibility"`
		Attributes    map[string]yamlText `yaml:"metadata"`
		AllowedTools  yaml.Node           `yaml:"allowed-tools"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(header.String()))
	if err := decoder.Decode(&wire); err != nil {
		return Metadata{}, fmt.Errorf("%w: frontmatter: %w", ErrInvalidSkill, err)
	}
	if err := decoder.Decode(new(yaml.Node)); err != io.EOF {
		return Metadata{}, fmt.Errorf("%w: expected one frontmatter document", ErrInvalidSkill)
	}
	metadata := Metadata{
		Name: string(wire.Name), Description: string(wire.Description),
		License: string(wire.License), Compatibility: string(wire.Compatibility),
	}
	if wire.Attributes != nil {
		metadata.Attributes = make(map[string]string, len(wire.Attributes))
		for key, value := range wire.Attributes {
			metadata.Attributes[key] = string(value)
		}
	}
	if err := validateName(metadata.Name); err != nil {
		return Metadata{}, fmt.Errorf("%w: name: %v", ErrInvalidSkill, err)
	}
	if strings.TrimSpace(metadata.Description) == "" || utf8.RuneCountInString(metadata.Description) > 1024 {
		return Metadata{}, fmt.Errorf("%w: description must contain 1 to 1024 characters", ErrInvalidSkill)
	}
	if utf8.RuneCountInString(metadata.Compatibility) > 500 {
		return Metadata{}, fmt.Errorf("%w: compatibility exceeds 500 characters", ErrInvalidSkill)
	}
	switch n := &wire.AllowedTools; n.Kind {
	case 0:
	case yaml.ScalarNode:
		if n.Tag != "!!str" {
			return Metadata{}, fmt.Errorf("%w: allowed-tools must be a string or string list", ErrInvalidSkill)
		}
		tools, err := splitAllowedTools(n.Value)
		if err != nil {
			return Metadata{}, err
		}
		metadata.AllowedTools = tools
	case yaml.SequenceNode:
		for _, item := range n.Content {
			if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || strings.TrimSpace(item.Value) == "" {
				return Metadata{}, fmt.Errorf("%w: allowed-tools entries must be non-blank strings", ErrInvalidSkill)
			}
			metadata.AllowedTools = append(metadata.AllowedTools, item.Value)
		}
	default:
		return Metadata{}, fmt.Errorf("%w: allowed-tools must be a string or string list", ErrInvalidSkill)
	}
	return metadata, nil
}

// YAML otherwise coerces numbers and booleans into Go strings. Metadata text
// should retain the author's declared type, including strings reached by aliases.
type yamlText string

func (s *yamlText) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return fmt.Errorf("expected a YAML string")
	}
	*s = yamlText(node.Value)
	return nil
}

func validateName(name string) error {
	if len(name) == 0 || len(name) > 64 || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") || strings.Contains(name, "--") {
		return fmt.Errorf("expected 1 to 64 lowercase letters, digits, and single internal hyphens")
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
			return fmt.Errorf("expected lowercase letters, digits, and hyphens")
		}
	}
	return nil
}

func splitAllowedTools(value string) ([]string, error) {
	var result []string
	depth, start := 0, 0
	for i, c := range value {
		switch c {
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("%w: unbalanced allowed-tools expression", ErrInvalidSkill)
			}
		case ' ', '\t', '\r', '\n', ',':
			if depth == 0 {
				if token := strings.TrimSpace(value[start:i]); token != "" {
					result = append(result, token)
				}
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("%w: unbalanced allowed-tools expression", ErrInvalidSkill)
	}
	if token := strings.TrimSpace(value[start:]); token != "" {
		result = append(result, token)
	}
	return result, nil
}
