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

const maxResourceBytes = 1 << 20

// FileSystem reads skills from an fs.FS without caching them. Concurrent calls
// are supported when the supplied filesystem supports concurrent reads.
// The caller owns the filesystem and its lifetime. Use os.Root.FS for local
// directories that need OS-enforced confinement; fs.FS alone is not a sandbox.
type FileSystem struct {
	fs fs.FS
}

// NewFileSystem creates a reader without accessing the filesystem.
func NewFileSystem(filesystem fs.FS) (*FileSystem, error) {
	if filesystem == nil {
		return nil, fmt.Errorf("skill: filesystem is required")
	}
	return &FileSystem{fs: filesystem}, nil
}

// List returns metadata sorted by skill name. Directories without SKILL.md are
// skipped; invalid skill documents are reported. Bodies and resources are not
// loaded. Each directory name must match the skill's frontmatter name.
func (f *FileSystem) List(ctx context.Context) ([]Metadata, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(f.fs, ".")
	if err != nil {
		return nil, fmt.Errorf("skill: list directory: %w", err)
	}
	result := []Metadata{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := fs.Stat(f.fs, path.Join(entry.Name(), "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, fileError(entry.Name(), err)
		}
		doc, err := f.readSkill(ctx, entry.Name(), false)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, doc.Metadata)
	}
	slices.SortFunc(result, func(a, b Metadata) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

// Load returns the instructions and sorted paths under references/, assets/,
// and scripts/. It does not read resource contents or execute scripts.
func (f *FileSystem) Load(ctx context.Context, name string) (Skill, error) {
	doc, err := f.readSkill(ctx, name, true)
	if err != nil {
		return Skill{}, err
	}
	doc.Resources = []string{}
	for _, directory := range []string{"references", "assets", "scripts"} {
		root := path.Join(name, directory)
		if err := f.checkLinks(root); errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return Skill{}, err
		}
		err := fs.WalkDir(f.fs, root, func(filePath string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				if filePath == root && errors.Is(walkErr, fs.ErrNotExist) {
					return nil
				}
				return walkErr
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				return fmt.Errorf("%w: symlink resource %q", ErrInvalidPath, filePath)
			}
			if filePath == root && !entry.IsDir() {
				return fmt.Errorf("%w: %q must be a directory", ErrInvalidPath, root)
			}
			if !entry.IsDir() {
				if !entry.Type().IsRegular() {
					return fmt.Errorf("%w: resource %q is not a regular file", ErrInvalidPath, filePath)
				}
				resourcePath := strings.TrimPrefix(filePath, name+"/")
				if !validResourcePath(resourcePath) {
					return fmt.Errorf("%w: resource %q", ErrInvalidPath, resourcePath)
				}
				doc.Resources = append(doc.Resources, resourcePath)
			}
			return nil
		})
		if err != nil {
			return Skill{}, fmt.Errorf("skill %q: list resources: %w", name, err)
		}
	}
	slices.Sort(doc.Resources)
	return doc, nil
}

// ReadResource returns a UTF-8 text file of at most 1 MiB. Resource paths must
// be clean, relative paths below references/, assets/, or scripts/.
func (f *FileSystem) ReadResource(ctx context.Context, name, resourcePath string) (string, error) {
	if !validResourcePath(resourcePath) {
		return "", fmt.Errorf("%w: resource %q must be within references/, assets/, or scripts/", ErrInvalidPath, resourcePath)
	}
	if _, err := f.readSkill(ctx, name, false); err != nil {
		return "", err
	}
	filePath := path.Join(name, resourcePath)
	file, err := f.open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxResourceBytes+1))
	if err != nil {
		return "", fmt.Errorf("skill: read %q: %w", filePath, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(data) > maxResourceBytes {
		return "", fmt.Errorf("%w: %q exceeds %d bytes", ErrTooLarge, filePath, maxResourceBytes)
	}
	if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return "", fmt.Errorf("%w: resource %q must be UTF-8 text", ErrInvalidSkill, filePath)
	}
	return string(data), nil
}

func validResourcePath(name string) bool {
	return fs.ValidPath(name) && !strings.Contains(name, "\\") &&
		(strings.HasPrefix(name, "references/") || strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, "scripts/"))
}

func (f *FileSystem) readSkill(ctx context.Context, name string, withBody bool) (Skill, error) {
	if err := ctx.Err(); err != nil {
		return Skill{}, err
	}
	if err := validateName(name); err != nil {
		return Skill{}, fmt.Errorf("%w: name %q: %v", ErrInvalidPath, name, err)
	}
	file, err := f.open(path.Join(name, "SKILL.md"))
	if err != nil {
		return Skill{}, err
	}
	defer file.Close()
	limited := &io.LimitedReader{R: file, N: maxSkillBytes + 1}
	reader := bufio.NewReader(limited)
	metadata, err := parseFrontmatter(reader)
	if limited.N == 0 {
		return Skill{}, fmt.Errorf("%w: %q exceeds %d bytes", ErrTooLarge, name, maxSkillBytes)
	}
	if err != nil {
		return Skill{}, fmt.Errorf("skill %q: %w", name, err)
	}
	if metadata.Name != name {
		return Skill{}, fmt.Errorf("%w: name %q does not match directory %q", ErrInvalidSkill, metadata.Name, name)
	}
	doc := Skill{Metadata: metadata}
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

func (f *FileSystem) open(name string) (fs.File, error) {
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

func (f *FileSystem) checkLinks(name string) error {
	// Lstat each component when supported, rejecting symlinks before opening.
	// The supplied filesystem must enforce confinement against concurrent edits.
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
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %q: %w", ErrNotFound, name, err)
	}
	return fmt.Errorf("skill: %q: %w", name, err)
}
