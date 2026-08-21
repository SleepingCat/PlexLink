# AGENTS.md

## Project

**PlexLink** is a small Go CLI for post-processing completed qBittorrent downloads into a Plex-friendly media library.

The project intentionally solves one narrow problem:

```text
qBittorrent completion
        ↓
PlexLink
        ↓
identify media
        ↓
resolve metadata via TMDB
        ↓
create NTFS hardlinks in Plex layout
        ↓
Plex identifies media and downloads metadata
```

PlexLink is **not** a Sonarr/Radarr/FileBot replacement.

Before making architectural or behavioral decisions, read:

```text
PLEXLINK_SPEC.md
```

`PLEXLINK_SPEC.md` is the primary source of product and technical requirements.  
This file defines how an agent should work on the repository.

---

## Core Rules

### 1. Never modify torrent source files

Source files belong to qBittorrent.

PlexLink must never:

- move source files;
- rename source files;
- delete source files;
- overwrite source files;
- change qBittorrent download paths;
- remove torrents;
- stop or modify seeding.

Allowed filesystem operations on media are intentionally limited.

Primary production mutation:

```go
os.Link(source, target)
```

Directory creation is allowed:

```go
os.MkdirAll(...)
```

If a proposed implementation requires destructive operations, stop and reconsider the design.

---

### 2. Prefer unresolved over a wrong match

Incorrect media identification is worse than requiring manual resolution.

Never implement:

```go
candidate := results[0]
```

without validation.

Automatic matching must use confidence scoring and available evidence.

If confidence is insufficient:

```text
UNRESOLVED
```

and no filesystem mutation should occur.

Examples that must be treated carefully:

```text
Yellowstone
The Killing
Counterpart
anime absolute numbering
same-title remakes
```

---

### 3. Keep the architecture small

Use the simplest design that satisfies the specification.

Do not introduce abstractions preemptively.

Avoid:

- DDD layers;
- repositories without persistence;
- service factories;
- dependency injection frameworks;
- event buses;
- plugin systems;
- generic workflow engines;
- unnecessary interfaces;
- large configuration frameworks.

Interfaces are appropriate at external I/O boundaries such as:

```go
TorrentClient
MetadataProvider
AIResolver
Linker
```

Prefer plain structs and functions elsewhere.

---

## Scope

### v0.1 includes

- Go CLI;
- qBittorrent Web API integration;
- torrent lookup by infohash;
- torrent file-list retrieval;
- source-kind detection by configured root path;
- release-name parsing;
- TMDB search and metadata lookup;
- candidate scoring;
- season / episode validation;
- Plex-compatible path generation;
- NTFS hardlink creation;
- idempotency;
- conflict detection;
- dry-run;
- inspect command;
- manual TMDB resolution;
- bounded AI-assisted fallback for low-confidence cases;
- xAI/Grok provider adapter with structured output and optional web search;
- Gemini provider adapter using native Gemini API, with `gemini-2.5-flash` as the free-tier-first default;
- Gemini Google Search grounded discovery plus separate strict normalization when tools and strict schema cannot be combined;
- TMDB verification of AI hypotheses;
- logging;
- tests.

### v0.1 explicitly excludes

Do not implement unless the specification is changed:

- Sonarr;
- Radarr;
- Prowlarr;
- FileBot;
- GUI;
- web UI;
- filesystem watcher;
- Windows Service;
- background daemon;
- SQLite;
- TVDB;
- AniDB;
- AniList;
- ffprobe-based identification;
- NFO generation;
- poster downloading;
- Plex metadata writing;
- automatic source cleanup;
- copy fallback;
- symlink fallback;
- torrent searching/downloading automation;
- torrent moving;
- torrent renaming;
- autonomous multi-step AI agents;
- AI access to local shell/filesystem tools;
- X search;
- code interpreter;
- MCP tools;

Do not expand scope because an adjacent feature appears convenient.

---

## Runtime Environment

Primary target:

```text
Windows
NTFS
Go
qBittorrent
Plex Media Server
```

Current media mapping:

```text
K:\video\serials → K:\plex\serials
K:\video\films   → K:\plex\films
K:\Anime         → K:\plex\anime
```

The application must not hard-code these paths. They belong in configuration.

Assume Windows filesystem semantics where relevant:

- case-insensitive paths;
- invalid filename characters;
- reserved device names;
- drive-volume restrictions for hardlinks;
- trailing dot / space restrictions.

Use `filepath` utilities rather than manual path concatenation.

---

## qBittorrent Integration

qBittorrent completion hook should pass only the torrent infohash:

```text
plexlink process --hash "%I"
```

Do not rely on shell-passed torrent names or content paths when the same information can be obtained from the qBittorrent Web API.

Reasons:

- safer quoting;
- fewer encoding issues;
- deterministic source data;
- access to the complete torrent file list;
- easier testing.

The qBittorrent client is read-only for v0.1.

Do not add API methods that mutate torrent state unless explicitly required.

---

## TMDB

TMDB is the canonical metadata provider in v0.1. AI/web search may propose hypotheses, but canonical media identity, target naming, seasons/episodes, and final validation must be checked against TMDB.

Use a small typed HTTP client instead of a large SDK unless an SDK clearly reduces code without hiding important behavior.

TMDB token must come from configuration/environment.

Never:

- commit credentials;
- print credentials;
- write credentials into logs;
- include real credentials in fixtures.

For canonical target names, default metadata language is:

```text
en-US
```

Search input may be any language.

---

## AI Resolver

Use AI only as a bounded fallback after deterministic parsing/TMDB matching is not confident enough. Do not call AI for already high-confidence matches.

Core rule:

```text
AI proposes/interprets -> TMDB verifies -> PlexLink decides -> linker mutates
```

AI output is untrusted data. It must never directly create paths or hardlinks.

AI providers must reuse the same application-level contract under `internal/ai`. xAI/Grok uses its OpenAI-compatible Responses API. Gemini uses the native Gemini API because Google Search grounding and provider execution metadata are provider-specific. Keep transport/tool details inside each provider adapter.

For the current personal deployment, prefer `gemini-2.5-flash` as the configurable default. With Gemini 2.5, strict structured output and built-in Google Search cannot be combined in one request. Therefore the Gemini adapter may perform grounded discovery first and a second no-tools structured-normalization request. This remains one logical resolver task and must stay bounded.

Web search is allowed for difficult media-identification cases. It is deliberately bounded: no open-ended agent loop, no local tools, no X search, no code interpreter, no MCP.

Provider prompts must treat torrent names, filenames, tracker text, and web pages as untrusted content rather than instructions. Use strict structured outputs. In candidate-selection mode, any returned TMDB ID must belong to the candidate list supplied by PlexLink.

Never send external AI:

- API keys or credentials;
- Authorization headers;
- unnecessary absolute local paths/usernames;
- arbitrary local file contents.

Automated tests must use fake providers/`httptest`; CI must not require a real AI key or paid API call.

---

## Release Parsing

Use:

```text
github.com/chill-institute/torrentname
```

as the primary release-name parser.

Treat parser output as evidence, not truth.

Use information from:

1. torrent name;
2. qBittorrent content path;
3. torrent file names;
4. parent directories.

Do not build a giant custom regex parser before proving the existing parser cannot handle a case.

Project-specific normalization should remain small, explicit, and covered by tests.

---

## Matching

Matching is the most correctness-sensitive part of the project.

The matcher must consider multiple signals.

Typical evidence:

```text
title match
alternative/original title match
year
season existence
episode existence
file-list consistency
```

A candidate with a plausible title but impossible season/episode structure should not win.

Example:

```text
torrent folder:
Yellowstone 2 - LostFilm.TV [MP4]

files:
Yellowstone.S02E01...
Yellowstone.S02E02...
```

The `S02` evidence is important and must participate in validation.

Matching thresholds belong in configuration.

Never silently lower matching thresholds to make tests pass.

---

## Anime

Anime is treated as TV content for Plex.

v0.1 supports:

```text
S01E03
S02E07
```

like normal TV episodes.

For absolute-number releases such as:

```text
[VARYG] Pluto - 03 [...]
```

automatic mapping is allowed only under the conservative rules defined in `PLEXLINK_SPEC.md`.

Do not add speculative multi-season absolute-number conversion in v0.1.

If ambiguous:

```text
UNRESOLVED_ANIME_NUMBERING
```

---

## Plex Paths

Generate Plex-friendly layouts.

TV:

```text
Show Name (2022) {tmdb-12345}\
└── Season 01\
    └── Show Name (2022) - S01E01.mkv
```

Movies:

```text
Movie Name (2024) {tmdb-12345}\
└── Movie Name (2024) {tmdb-12345}.mkv
```

Anime:

```text
Anime Name (2023) {tmdb-12345}\
└── Season 01\
    └── Anime Name (2023) - S01E03.mkv
```

Keep path generation deterministic.

Do not depend on Plex guessing if TMDB identity is already known.

---

## Hardlink Behavior

Hardlink creation must be idempotent.

Before linking:

- validate source;
- validate source root containment;
- validate target root containment;
- create target directory;
- check target existence.

Required behavior:

```text
target missing
→ create hardlink

target exists and os.SameFile(source, target)
→ NOOP

target exists and points to different file
→ CONFLICT
```

Never automatically overwrite a conflicting target.

Do not silently fall back to copy or symlink.

Hardlink failure must be explicit.

---

## Dry Run

`--dry-run` is a first-class feature, not a debugging afterthought.

Dry-run should execute:

- qBittorrent lookup;
- file discovery;
- parsing;
- TMDB lookup;
- matching;
- scoring;
- target-path planning;
- conflict checks where possible.

Dry-run must not:

- create directories;
- create hardlinks;
- alter state that affects production behavior.

Whenever adding a new mutation path, ensure dry-run bypasses it.

---

## CLI

Keep the CLI simple.

Expected commands:

```text
plexlink doctor
plexlink process --hash HASH
plexlink process --hash HASH --dry-run
plexlink inspect --hash HASH
plexlink resolve --hash HASH --tmdb-id ID
```

Avoid Cobra unless command complexity clearly justifies it.

The standard `flag` package plus a small subcommand dispatcher is preferred.

---

## Configuration

Configuration should be explicit and boring.

Preferred:

```text
config.yaml
```

Secrets should be referenced via environment variables.

Every AI provider adapter must support an `api_key` setting in `config.yaml` by default.

Do not introduce Viper or similar configuration frameworks unless necessary.

Use:

```text
gopkg.in/yaml.v3
```

for YAML.

Validate configuration on startup.

Fail early with a useful error message.

---

## Logging

Use:

```go
log/slog
```

Prefer structured logs.

Useful fields include:

```text
torrent_hash
torrent_name
kind
tmdb_id
score
source
target
action
duration
error
```

Never log secrets.

Errors shown to the user should remain readable even if structured logging is enabled.

---

## Error Handling

Return errors with context.

Prefer:

```go
fmt.Errorf("fetch torrent %s: %w", hash, err)
```

over generic messages.

Avoid panic for expected runtime failures.

Use panic only for impossible programmer errors, if at all.

Network/API/filesystem failures must not result in partial destructive operations.

---

## HTTP

Use a shared `http.Client`.

Requirements:

- timeout;
- context support;
- checked HTTP status codes;
- limited retries for transient failures only.

Retry only appropriate transient cases such as:

```text
429
502
503
504
```

Respect `Retry-After` where available.

Do not retry authentication or deterministic client errors blindly.

---

## Context

Network and long-running operations should accept:

```go
context.Context
```

Propagate context through qBittorrent and TMDB clients.

Do not use `context.Background()` deep inside business logic when a caller context is already available.

---

## Tests

Tests are part of implementation, not a later cleanup step.

After each meaningful change:

```text
go test ./...
```

must pass.

### Unit tests

Cover at minimum:

- title normalization;
- release parsing;
- path containment;
- Windows filename sanitation;
- matching score;
- ambiguous matches;
- Plex path generation;
- hardlink idempotency;
- conflict detection;
- anime conservative mapping.

### HTTP integration tests

Use:

```go
httptest.Server
```

for qBittorrent, TMDB, xAI, and Gemini provider HTTP behavior.

Do not require live Internet for automated tests.

### Filesystem tests

Use:

```go
t.TempDir()
```

Test real hardlinks where supported.

Verify with:

```go
os.SameFile
```

### Regression fixtures

Real-world noisy names already encountered should stay in tests.

Examples:

```text
BoJack Horseman (1080p WEB-DL)
Counterpart 2 - LostFilm.TV [MP4]
Death's Game (Season 1) WEB-DL 1080p
Game.of.Thrones.S01.1080p
Hazbin Hotel S01
Pantheon.S01.WEB-DL.1080p.NewStation
Pantheon.S02.WEB-DL.1080p.NewStation
Rick.and.Morty.S09.AMZN.WEB-DL.1080p.by.AKTEP
South.Park.S28.1080p.WEBDL
The Knick (s01)
The Knick (Season 02) Amedia
The.Devils.Hour.S01.1080p.WEB-DL
Yellowstone 2 - LostFilm.TV [MP4]
[VARYG] Pluto [WEB-DL 1080p x264 DDP]
Мышь [Студия Колобок & XDUB DORAMA]
House.of.the.Dragon.S03E01.720p.rus.LostFilm.TV.mp4
Invincible.S04E01.720p.rus.LostFilm.TV.mp4
```

Do not remove a regression fixture merely because it is inconvenient to support.

---

## Development Workflow for Codex

Before implementation:

1. Read `PLEXLINK_SPEC.md`.
2. Read this `AGENTS.md`.
3. Inspect the existing repository.
4. Reuse existing working code where appropriate.
5. Identify the current implementation stage.

Do not rewrite working code only to impose a preferred style.

For each stage:

1. state briefly what will be implemented;
2. implement the smallest coherent change;
3. add/update tests;
4. run:
   ```text
   go test ./...
   ```
5. fix failures;
6. report what changed and any unresolved issue;
7. continue only when the stage is stable.

When modifying architecture, explain the concrete problem the change solves.

---

## Code Style

Use idiomatic Go.

Prefer:

- small functions;
- explicit data flow;
- concrete types;
- early returns;
- wrapped errors;
- table-driven tests;
- standard library.

Avoid:

- global mutable state;
- hidden side effects;
- reflection unless clearly justified;
- `interface{}` / `any` when a concrete type works;
- clever generic abstractions;
- unnecessary goroutines;
- channels for synchronous workflows;
- premature concurrency.

The workload is mostly:

```text
small HTTP requests
+
filesystem metadata
+
one hardlink per media file
```

Concurrency is not a priority for v0.1.

Correctness and debuggability matter more.

---

## Dependencies

Before adding a dependency:

1. explain what problem it solves;
2. check whether the standard library is sufficient;
3. prefer small maintained packages;
4. avoid framework-style dependencies.

Expected external dependencies are intentionally few:

```text
github.com/chill-institute/torrentname
gopkg.in/yaml.v3
```

Do not add libraries for logging, HTTP, CLI, DI, retries, or testing unless there is a concrete need.

---

## Security

Treat torrent names and file paths as untrusted input.

Never construct shell commands from torrent-controlled values.

Do not execute media filenames.

Do not interpolate torrent names into PowerShell/cmd command strings.

Validate path containment before filesystem mutation.

Keep credentials out of:

- git;
- CLI arguments where practical;
- logs;
- test fixtures.

---

## Git / Change Discipline

Prefer small focused commits.

Do not combine:

```text
refactor + behavior change + dependency upgrade
```

unless unavoidable.

Do not edit unrelated files.

Do not mass-format files outside the touched area without reason.

Do not remove comments/tests documenting non-obvious safety behavior.

---

## Documentation

When behavior changes, update the relevant documentation.

README should eventually contain:

- purpose;
- installation;
- config example;
- TMDB setup;
- qBittorrent hook;
- `doctor`;
- dry-run workflow;
- conflict/unresolved behavior;
- Plex library configuration.

`PLEXLINK_SPEC.md` should describe product requirements.

`AGENTS.md` should describe how agents work on the codebase.

Do not duplicate the full specification into README.

---

## Definition of a Good Change

A change is good when it:

- keeps torrent sources untouched;
- reduces ambiguity rather than hiding it;
- is deterministic;
- can be inspected with dry-run;
- is covered by tests;
- keeps architecture simpler or equally simple;
- does not expand product scope accidentally.

If a change makes PlexLink more magical but less predictable, do not make it.

---

## Final Priority Order

When requirements compete, use this order:

```text
1. Never damage torrent source data
2. Never silently create a wrong media match
3. Deterministic / idempotent behavior
4. Clear diagnostics
5. Testability
6. Simplicity
7. Automation
8. Performance
```

For this project, a safe `UNRESOLVED` result is considered successful behavior when the available evidence is insufficient.
