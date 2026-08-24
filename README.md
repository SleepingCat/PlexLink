# PlexLink

PlexLink is a small Go CLI that post-processes completed qBittorrent downloads. It identifies movies and TV/anime through TMDB and creates NTFS hardlinks in Plex-compatible directories. Torrent-owned source files are never moved, renamed, overwritten, or deleted.

## Build

Go 1.26 or newer is required.

```sh
go test ./...
go build -o plexlink.exe ./cmd/plexlink
```

## Release

Push a semantic version tag to publish a Windows amd64 release:

```powershell
git tag v1.0.0
git push origin v1.0.0
```

GitHub Actions tests the tagged revision and creates a release containing the Windows ZIP and its SHA-256 checksum automatically.

Copy `config.example.yaml` to the gitignored `config.yaml` and adjust all paths. Secrets can be referenced through environment variables:

```powershell
$env:PLEXLINK_QBT_PASSWORD = "..."
$env:PLEXLINK_TMDB_TOKEN = "..."
$env:PLEXLINK_XAI_API_KEY = "..." # only when ai.enabled is true
$env:PLEXLINK_GROQ_API_KEY = "..." # when ai.provider is groq
```

For a precompiled executable, secrets may instead be stored directly in the local configuration:

```yaml
qbittorrent:
  password: "..."
tmdb:
  token: "..."
```

Use exactly one form for each secret: `password` or `password_env`, and `token` or `token_env`. A configuration containing literal secrets must remain outside Git and should be readable only by the account running PlexLink.

For xAI, use exactly one of `ai.xai.api_key_env` or a literal key in a local gitignored configuration:

```yaml
ai:
  enabled: true
  xai:
    api_key: "xai-..."
```

The TMDB token is an API Read Access Token (Bearer token). PlexLink uses `en-US` canonical names by default; Plex controls the display metadata language independently.

## Commands

```text
plexlink doctor [--config config.yaml]
plexlink process --hash INFOHASH [--dry-run] [--no-ai]
plexlink inspect --hash INFOHASH
plexlink resolve --hash INFOHASH --tmdb-id ID
```

Start with `doctor`, then test a real completed torrent with `process --dry-run`. `inspect` emits the full JSON evidence, candidates, score and link plan. A successful `resolve` remembers the explicit TMDB ID for that torrent hash in `state/resolutions.yaml`.

AI fallback is disabled in `config.example.yaml`. When explicitly enabled, PlexLink uses the configured provider only after deterministic evidence is insufficient or to enrich a non-canonical episode mapping. AI may propose search queries or episode mappings, but PlexLink searches catalogs again and requires deterministic candidate agreement plus final TMDB verification before accepting canonical metadata. Provider timeouts, rate limits, server/auth errors, and invalid output are recorded as degraded diagnostics; they do not invalidate deterministic results or provisional mappings. `--no-ai` disables the fallback for one invocation. Structured AI results are cached under `state/ai-cache`; API keys, Authorization headers, absolute local paths, and raw responses are not stored.

Groq is an optional identity-only consultant. Select it with `ai.provider: groq` and `ai.web_search: require`; it uses `groq/compound-mini` with one bounded web-search request and is never called per episode. Its title/year hypothesis enters the same catalog requery and TMDB verification path as other consultants and cannot directly create a match or link. When normal scoring remains insufficient, a unique non-conflicting catalog candidate that agrees with the high-confidence web-backed hypothesis may be reported explicitly as `AI_ASSISTED_MATCH`; its deterministic score is not increased.

Configure qBittorrent's completion hook only after dry-run validation:

```text
"C:\path\plexlink.exe" process --hash "%I"
```

Point Plex TV libraries at the configured TV and anime targets, and the Plex Movie library at the movie target. Do not point PlexLink at the torrent source directories as targets.

## Safety behavior

- Low confidence or an insufficient score margin returns `UNRESOLVED` and creates no media links.
- After show identity is accepted, files are mapped independently: canonical files are `RESOLVED`, a strongly supported source `SxxEyy` missing from provider metadata may be `PROVISIONAL`, and unsafe files remain `UNRESOLVED` without blocking safe siblings.
- `RESOLVED_WITH_WARNINGS` means the plan includes provisional files; `PARTIAL` means safe siblings were planned while at least one file remained unresolved.
- Anime absolute numbering is accepted only for one non-special TMDB season and an in-range episode.
- An existing target hardlinked to the same source is a `NOOP`.
- An existing target belonging to another file is a `CONFLICT`; it is never overwritten.
- Dry-run creates neither directories nor hardlinks and does not persist unresolved reports.

Exit codes are stable: `0` success, `10` ignored, `20` unresolved, `21` anime numbering unresolved, `30` conflict, `40` configuration, `41` qBittorrent, `42` TMDB, `43` an operational AI failure when fallback was needed, and `50` filesystem/hardlink failure.
