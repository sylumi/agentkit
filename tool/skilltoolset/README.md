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
root, err := os.OpenRoot("./skills")
if err != nil {
    return err
}
defer root.Close()

skills, err := skilltoolset.New(skilltoolset.Config{FS: root.FS()})
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

The caller owns the filesystem and its lifetime. `embed.FS` and `fstest.MapFS`
also work. Calls may run concurrently when the supplied filesystem supports it;
results are independent copies. There is no shared activation state or cache.

## Reading behavior

- Discovery scans immediate subdirectories. Directories without SKILL.md are
  ignored; malformed skills and directory/name mismatches return errors.
- Loading a skill lists files under `references/`, `assets/`, and `scripts/`.
  Resource content is read separately. Reading a script does not execute it.
- SKILL.md loads are limited to 256 KiB; individual resources to 1 MiB. Both
  must be UTF-8 text without NUL bytes. Oversized content is rejected, not truncated.
- Paths must be clean relative paths. Symlinks are rejected when exposed by
  the filesystem. The supplied filesystem controls confinement against concurrent
  filesystem changes; use `os.Root.FS()` for that guarantee on supported platforms.
- Cancellation is checked between filesystem operations. An arbitrary fs.FS
  implementation may perform blocking I/O that cannot be interrupted.
- `allowed-tools` is metadata; it does not change the application's permissions
  or register additional tools.

Errors preserve `skill.ErrNotFound`, `skill.ErrInvalidSkill`,
`skill.ErrInvalidPath`, `skill.ErrTooLarge`, and underlying filesystem causes
where applicable.
