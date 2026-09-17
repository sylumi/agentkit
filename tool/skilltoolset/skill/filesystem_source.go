package skill

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"unicode/utf8"
)

// NewFileSystemSource creates an uncached source without accessing the filesystem.
// Skills are immediate subdirectories containing SKILL.md. The filesystem must be non-nil.
func NewFileSystemSource(filesystem fs.FS) Source {
	return &fileSystemSource{fs: filesystem}
}

type fileSystemSource struct {
	fs fs.FS
}

// ListFrontmatters skips directories without SKILL.md and reports invalid skills.
func (f *fileSystemSource) ListFrontmatters(ctx context.Context) ([]Frontmatter, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(f.fs, ".")
	if err != nil {
		return nil, fmt.Errorf("skill: list directory: %w", err)
	}
	result := []Frontmatter{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		if _, err := fs.Stat(f.fs, path.Join(entry.Name(), "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fileError(entry.Name(), err)
		}
		doc, err := f.readSkill(ctx, entry.Name(), false)
		if errors.Is(err, ErrSkillNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, doc.Frontmatter)
	}
	slices.SortFunc(result, func(a, b Frontmatter) int { return strings.Compare(a.Name, b.Name) })
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (f *fileSystemSource) LoadFrontmatter(ctx context.Context, name string) (Frontmatter, error) {
	doc, err := f.readSkill(ctx, name, false)
	return doc.Frontmatter, err
}

func (f *fileSystemSource) LoadInstructions(ctx context.Context, name string) (string, error) {
	doc, err := f.readSkill(ctx, name, true)
	return doc.Instructions, err
}

func (f *fileSystemSource) ListResources(ctx context.Context, name, subpath string) ([]string, error) {
	if _, err := f.LoadFrontmatter(ctx, name); err != nil {
		return nil, err
	}
	all := subpath == "" || subpath == "."
	if !all && !validResourceSubpath(subpath) {
		return nil, fmt.Errorf("%w: resource subpath %q", ErrInvalidPath, subpath)
	}
	targets := []string{subpath}
	if all {
		targets = []string{"references", "assets", "scripts"}
	}
	resources := []string{}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root := path.Join(name, target)
		if err := f.checkLinks(root); err != nil {
			if all && errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, missingError(ErrResourceNotFound, err)
		}
		err := fs.WalkDir(f.fs, root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if all && filePath == root && errors.Is(walkErr, fs.ErrNotExist) {
					return nil
				}
				return walkErr
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%w: symlink resource %q", ErrInvalidPath, filePath)
			}
			if !entry.IsDir() {
				if !entry.Type().IsRegular() {
					return fmt.Errorf("%w: resource %q is not a regular file", ErrInvalidPath, filePath)
				}
				resourcePath := strings.TrimPrefix(filePath, name+"/")
				if !validResourcePath(resourcePath) {
					return fmt.Errorf("%w: resource %q", ErrInvalidPath, resourcePath)
				}
				resources = append(resources, resourcePath)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("skill %q: list resources: %w", name, missingError(ErrResourceNotFound, err))
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.Sort(resources)
	return resources, nil
}

func (f *fileSystemSource) LoadResource(ctx context.Context, name, resourcePath string) (io.ReadCloser, error) {
	if _, err := f.LoadFrontmatter(ctx, name); err != nil {
		return nil, err
	}
	if !validResourcePath(resourcePath) {
		return nil, fmt.Errorf("%w: resource %q must be within references/, assets/, or scripts/", ErrInvalidPath, resourcePath)
	}
	file, err := f.open(path.Join(name, resourcePath))
	if err != nil {
		return nil, missingError(ErrResourceNotFound, err)
	}
	if err := ctx.Err(); err != nil {
		file.Close()
		return nil, err
	}
	return &resourceStream{ctx: ctx, ReadCloser: file}, nil
}

type resourceStream struct {
	ctx context.Context
	io.ReadCloser
}

func (s *resourceStream) Read(p []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	return s.ReadCloser.Read(p)
}

func validResourceSubpath(name string) bool {
	return name == "references" || name == "assets" || name == "scripts" || validResourcePath(name)
}

func missingError(kind, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %w", kind, err)
	}
	return err
}

func validResourcePath(name string) bool {
	return fs.ValidPath(name) && !strings.Contains(name, "\\") &&
		(strings.HasPrefix(name, "references/") || strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, "scripts/"))
}

func (f *fileSystemSource) readSkill(ctx context.Context, name string, withBody bool) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	if err := validateName(name); err != nil {
		return Skill{}, fmt.Errorf("%w: name %q: %v", ErrInvalidPath, name, err)
	}
	file, err := f.open(path.Join(name, "SKILL.md"))
	if err != nil {
		return Skill{}, missingError(ErrSkillNotFound, err)
	}
	defer file.Close()
	limited := &io.LimitedReader{R: &resourceStream{ctx: ctx, ReadCloser: file}, N: maxSkillBytes + 1}
	reader := bufio.NewReader(limited)
	frontmatter, err := parseFrontmatter(reader)
	if limited.N == 0 {
		return Skill{}, fmt.Errorf("%w: %q exceeds %d bytes", ErrTooLarge, name, maxSkillBytes)
	}
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", name, err)
	}
	if frontmatter.Name != name {
		return Skill{}, fmt.Errorf("%w: name %q does not match directory %q", ErrInvalidSkill, frontmatter.Name, name)
	}
	doc := Skill{Frontmatter: frontmatter}
	if withBody {
		body, err := io.ReadAll(reader)
		if err != nil {
			return Skill{}, fmt.Errorf("skill %q: read instructions: %w", name, err)
		}
		if limited.N == 0 {
			return Skill{}, fmt.Errorf("%w: %q exceeds %d bytes", ErrTooLarge, name, maxSkillBytes)
		}
		if !utf8.Valid(body) || bytes.ContainsRune(body, 0) {
			return Skill{}, fmt.Errorf("%w: instructions must be UTF-8 text", ErrInvalidSkill)
		}
		doc.Instructions = string(body)
	}
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	return doc, nil
}

func (f *fileSystemSource) open(name string) (fs.File, error) {
	if err := f.checkLinks(name); err != nil {
		return nil, err
	}
	file, err := f.fs.Open(name)
	if err != nil {
		return nil, fileError(name, err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err != nil {
			return nil, fileError(name, err)
		}
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrInvalidPath, name)
	}
	return file, nil
}

func (f *fileSystemSource) checkLinks(name string) error {
	// Reject symlinks in each path component when Lstat is supported.
	if links, ok := f.fs.(fs.ReadLinkFS); ok {
		current := ""
		for component := range strings.SplitSeq(name, "/") {
			current = path.Join(current, component)
			info, err := links.Lstat(current)
			if err != nil {
				return fileError(name, err)
			}
			if info.Mode()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%w: symlink %q", ErrInvalidPath, current)
			}
		}
	}
	return nil
}

func fileError(name string, err error) error {
	return fmt.Errorf("skill: %q: %w", name, err)
}
