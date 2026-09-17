package skilltool

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"unicode/utf8"

	"github.com/sylumi/agentkit/tool"
	"github.com/sylumi/agentkit/tool/functiontool"
	"github.com/sylumi/agentkit/tool/skilltoolset/skill"
)

const ResourceName = "load_skill_resource"

const maxResourceBytes = 1 << 20

type ResourceArgs struct {
	Name string `json:"name" jsonschema:"The name of the skill that owns the resource."`
	Path string `json:"path" jsonschema:"The relative resource path returned by load_skill, such as references/guide.md."`
}

type ResourceResult struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

func LoadSkillResource(source skill.Source) (tool.Tool, error) {
	return functiontool.New(
		functiontool.Config{
			Name:        ResourceName,
			Description: "Read a skill's UTF-8 text resource from references/, assets/, or scripts/. Reading a script does not execute it.",
		}, func(ctx context.Context, args ResourceArgs) (ResourceResult, error) {
			return loadSkillResource(ctx, args, source)
		})
}

// loadSkillResource reads at most 1 MiB of UTF-8 text and closes the stream.
func loadSkillResource(ctx context.Context, args ResourceArgs, source skill.Source) (ResourceResult, error) {
	if err := ctx.Err(); err != nil {
		return ResourceResult{}, err
	}
	if !validResourcePath(args.Path) {
		return ResourceResult{}, fmt.Errorf("%w: resource %q must be within references/, assets/, or scripts/", skill.ErrInvalidPath, args.Path)
	}
	stream, err := source.LoadResource(ctx, args.Name, args.Path)
	if err != nil {
		return ResourceResult{}, err
	}
	defer stream.Close()
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: stream}, maxResourceBytes+1))
	if err != nil {
		return ResourceResult{}, fmt.Errorf("skill %q: read resource %q: %w", args.Name, args.Path, err)
	}
	if err := ctx.Err(); err != nil {
		return ResourceResult{}, err
	}
	if len(data) > maxResourceBytes {
		return ResourceResult{}, fmt.Errorf("%w: resource %q exceeds %d bytes", skill.ErrTooLarge, args.Path, maxResourceBytes)
	}
	if !utf8.Valid(data) || bytes.ContainsRune(data, 0) {
		return ResourceResult{}, fmt.Errorf("%w: resource %q must be UTF-8 text", skill.ErrInvalidSkill, args.Path)
	}
	return ResourceResult{Name: args.Name, Path: args.Path, Content: string(data)}, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func validResourcePath(name string) bool {
	return fs.ValidPath(name) && !strings.Contains(name, "\\") &&
		(strings.HasPrefix(name, "references/") || strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, "scripts/"))
}
