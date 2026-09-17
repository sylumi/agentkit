package skill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

type mergedSource struct {
	sources []Source
}

// NewMergedSource combines non-nil sources in lookup order, copying the slice.
// Listing rejects duplicate names; reads advance only on ErrSkillNotFound.
func NewMergedSource(sources ...Source) Source {
	return &mergedSource{sources: slices.Clone(sources)}
}

func (m *mergedSource) ListFrontmatters(ctx context.Context) ([]Frontmatter, error) {
	result := []Frontmatter{}
	names := make(map[string]bool)
	for _, source := range m.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		frontmatters, err := source.ListFrontmatters(ctx)
		if err != nil {
			return nil, err
		}
		for _, fm := range frontmatters {
			if names[fm.Name] {
				return nil, fmt.Errorf("%w: %q", ErrDuplicateSkill, fm.Name)
			}
			names[fm.Name] = true
			result = append(result, fm)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(result, func(a, b Frontmatter) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

func (m *mergedSource) LoadFrontmatter(ctx context.Context, name string) (Frontmatter, error) {
	for _, source := range m.sources {
		if err := ctx.Err(); err != nil {
			return Frontmatter{}, err
		}
		fm, err := source.LoadFrontmatter(ctx, name)
		if !errors.Is(err, ErrSkillNotFound) {
			return fm, err
		}
	}
	return Frontmatter{}, m.notFound(ctx, name)
}

func (m *mergedSource) LoadInstructions(ctx context.Context, name string) (string, error) {
	for _, source := range m.sources {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		instructions, err := source.LoadInstructions(ctx, name)
		if !errors.Is(err, ErrSkillNotFound) {
			return instructions, err
		}
	}
	return "", m.notFound(ctx, name)
}

func (m *mergedSource) ListResources(ctx context.Context, name, subpath string) ([]string, error) {
	for _, source := range m.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resources, err := source.ListResources(ctx, name, subpath)
		if !errors.Is(err, ErrSkillNotFound) {
			return resources, err
		}
	}
	return nil, m.notFound(ctx, name)
}

func (m *mergedSource) LoadResource(ctx context.Context, name, resourcePath string) (io.ReadCloser, error) {
	for _, source := range m.sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		resource, err := source.LoadResource(ctx, name, resourcePath)
		if !errors.Is(err, ErrSkillNotFound) {
			return resource, err
		}
	}
	return nil, m.notFound(ctx, name)
}

func (m *mergedSource) notFound(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("%w: %q", ErrSkillNotFound, name)
}
