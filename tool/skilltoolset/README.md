# Skill tools

`skilltoolset` exposes three ordinary `tool.Tool` implementations:

| Tool | Arguments | Result |
| --- | --- | --- |
| `list_skills` | `{}` | Skill names and descriptions |
| `load_skill` | `{"name":"greeting"}` | Frontmatter, Markdown instructions, and resource paths |
| `load_skill_resource` | `{"name":"greeting","path":"references/greeting.txt"}` | Resource name, path, and text content |

The filesystem root contains one directory per skill:

```text
skills/
  greeting/
    SKILL.md
    references/greeting.txt
```

`SKILL.md` starts with YAML frontmatter delimited by `---` lines. `name` and
`description` are required. Names use 1–64 lowercase ASCII letters, digits, and
single internal hyphens, and must match their directory. Descriptions contain
1–1024 characters. Optional license, compatibility, metadata, and allowed-tools
fields are preserved. Both scalar and string-list allowed-tools forms are accepted.

Following the [Agent Skills specification](https://agentskills.io/specification), `skill.Frontmatter` represents the whole YAML header, while its `Metadata` field holds the optional `metadata` key-value map. `skill.Skill.Frontmatter` is returned under the `frontmatter` JSON key by `load_skill`, so custom metadata appears at `frontmatter.metadata`.

## Register with a model request

```go
source := skill.NewFileSystemSource(os.DirFS("./skills"))
skills, err := skilltoolset.New(skilltoolset.Config{Source: source})
if err != nil {
    return err
}
instructions, err := skills.Instructions(ctx)
if err != nil {
    return err
}
req := model.Request{
    Instructions: baseInstructions + "\n\n" + instructions,
    Messages: history,
}
for _, implementation := range skills.Tools() {
    req.Tools = append(req.Tools, implementation.Definition())
}
```

Build instructions from the application's base text for each request. The catalog
contains only skill names and descriptions. Bodies and resource contents enter
the conversation through tool results when requested. Dispatch a model's tool
call to the matching tool's `Execute`, then associate its content/error with the
call ID using the application's normal tool-result handling.

The caller owns the source or filesystem and its lifetime. `embed.FS` and `fstest.MapFS` also work. Calls may run concurrently when the supplied source supports it; results are independent copies. There is no shared activation state or cache.

## Configure the name and guidance

```go
skills, err := skilltoolset.New(skilltoolset.Config{
    Source: source,
    Name: "ProjectSkills",
    SystemInstruction: "Load a relevant skill before answering.",
})
if err != nil {
    return err
}
```

`Name()` returns the configured name, defaulting to `"SkillToolset"`. The three model-facing tool names stay the same. A non-empty `SystemInstruction` replaces the built-in skill guidance; `Instructions(ctx)` appends the generated skill catalog. An empty value uses the default guidance. If no skills are available, `Instructions(ctx)` returns an empty string even with custom guidance.

## Select and combine sources

`Config.Source` is required and accepts any `skill.Source`. `NewFileSystemSource` wraps a non-nil `fs.FS` and returns a source directly. Neither constructor reads skill content; filesystem access errors are reported when content is requested.

```go
projectSource := skill.NewFileSystemSource(projectRoot.FS())
personalSource := skill.NewFileSystemSource(personalRoot.FS())
skills, err := skilltoolset.New(skilltoolset.Config{
    Source: skill.NewMergedSource(projectSource, personalSource),
})
if err != nil {
    return err
}
```

Here `projectRoot` and `personalRoot` are open `*os.Root` values, kept open for the toolset's lifetime. Sources are queried in the given order. Listing returns a catalog sorted by name and rejects duplicates, including duplicates within a source. Direct reads use the first matching skill; only `ErrSkillNotFound` permits looking in the next source. Missing resources, invalid skills, and permission failures stop the lookup. Resources from same-name skills are never combined. Call `Instructions(ctx)` before model use to detect duplicate names.

`Source` separates access into five operations:

| Method | Result |
| --- | --- |
| `ListFrontmatters(ctx)` | Sorted skill headers |
| `LoadFrontmatter(ctx, name)` | One skill's header |
| `LoadInstructions(ctx, name)` | The Markdown body |
| `ListResources(ctx, name, subpath)` | Sorted resource paths relative to the skill |
| `LoadResource(ctx, name, path)` | An `io.ReadCloser` owned by the caller |

For `ListResources`, `""` and `"."` list all files under `references`, `assets`, and `scripts`, skipping absent optional directories. A clean subpath selects a resource directory, nested directory, or single file. An explicitly requested missing path returns `ErrResourceNotFound`.

Raw resource streams may contain binary or large files. Direct consumers must close each stream and choose their own reading limits. The resource tool closes streams and applies the text rules below for every source, including custom implementations. Source calls do not form an atomic snapshot; filesystem content can change between calls.

Custom sources must return independent values, including nested maps and slices, honor cancellation, and support concurrent reads when their backing store does. `LoadResource` returns a non-nil stream on success and no stream on error. Each source passed to `NewMergedSource` must be non-nil; supplying no sources creates an empty catalog.

## Reading behavior

- Discovery scans immediate subdirectories. Directories without SKILL.md are
  ignored; malformed skills and directory/name mismatches return errors.
- Loading a skill lists files under `references/`, `assets/`, and `scripts/`.
  Resource content is read separately. Reading a script does not execute it.
- Filesystem SKILL.md loads are limited to 256 KiB and must be UTF-8 text without NUL bytes. The resource tool accepts at most 1 MiB of UTF-8 text without NUL bytes from any source. Oversized content is rejected, not truncated; raw `Source.LoadResource` streams do not apply this text limit.
- Paths must be clean relative paths. Symlinks are rejected when exposed by
  the filesystem. The supplied filesystem controls confinement against concurrent
  filesystem changes; use `os.Root.FS()` for that guarantee on supported platforms.
- Cancellation is checked between filesystem operations and resource reads. Keep the context alive until a resource stream is closed. An arbitrary source or fs.FS implementation may perform blocking I/O that cannot be interrupted.
- `allowed-tools` is metadata; it does not change the application's permissions
  or register additional tools.

Errors can be checked with `errors.Is` against `skill.ErrSkillNotFound`, `skill.ErrResourceNotFound`, `skill.ErrDuplicateSkill`, `skill.ErrInvalidSkill`, `skill.ErrInvalidPath`, and `skill.ErrTooLarge`. Filesystem errors preserve their underlying causes where applicable. Custom sources must distinguish missing skills from missing resources so merged lookups select the correct owner.
