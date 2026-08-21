# PlexLink — техническая спецификация для реализации

## 0. Цель

Нужно реализовать небольшую CLI-утилиту на Go, которая заменяет FileBot **только в нужном сценарии**:

1. Пользователь сам выбирает раздачу на торрент-трекере.
2. qBittorrent скачивает её в одну из существующих папок:
   - `K:\video\serials`
   - `K:\video\films`
   - `K:\Anime`
3. После завершения qBittorrent вызывает `plexlink`.
4. `plexlink`:
   - получает информацию о торренте через qBittorrent Web API;
   - определяет тип контента по исходной папке;
   - разбирает release name и имена файлов;
   - определяет точный фильм/сериал через TMDB;
   - строит структуру, рекомендованную Plex;
   - создаёт **NTFS hardlinks**, не перемещая и не изменяя исходные файлы.
5. Plex смотрит только на:
   - `K:\plex\serials`
   - `K:\plex\films`
   - `K:\plex\anime`
6. Plex TV Series / Plex Movie сам загружает постеры, описания и остальные метаданные.

Главная идея: **не писать аналог Sonarr/FileBot**. Утилита должна решать одну задачу — превращать хаотичную структуру торрент-раздачи в стабильную Plex-библиотеку через hardlinks.

---

# 1. Основные ограничения и принципы

## 1.1. KISS

Не нужны:

- Web UI;
- daemon/service в MVP;
- собственная база фильмов/сериалов;
- автоматический поиск торрентов;
- управление qBittorrent;
- скачивание постеров;
- изменение/переименование исходных файлов;
- удаление файлов;
- полноценный Sonarr/FileBot clone;
- сложная DDD-архитектура.

Нужен event-driven CLI:

```text
qBittorrent completion
        ↓
plexlink process --hash <infohash>
        ↓
qBittorrent API
        ↓
parse → resolve → hardlink
        ↓
Plex
```

## 1.2. Никаких destructive operations

MVP **никогда** не должен:

- `os.Remove`;
- `os.Rename` для исходного контента;
- перемещать torrent data;
- менять путь раздачи;
- перезаписывать существующий target-файл;
- удалять существующие Plex-файлы.

Разрешённые filesystem actions:

```go
os.MkdirAll(...)
os.Link(source, target)
```

Плюс чтение файлов/метаданных.

## 1.3. Idempotency

Один и тот же torrent completion event может прийти повторно.

Повторный запуск должен быть безопасным:

- если target отсутствует → создать hardlink;
- если target уже является hardlink на тот же source → `NOOP`;
- если target существует, но указывает на другой файл → `CONFLICT`, ничего не перезаписывать.

Повторный `process --hash ...` должен давать тот же результат.

---

# 2. Целевая файловая структура

## Sources — принадлежат qBittorrent

```text
K:\
├── video\
│   ├── serials\
│   └── films\
└── Anime\
```

Исходные torrent-файлы остаются здесь навсегда или пока пользователь сам не удалит раздачу.

## Targets — принадлежат PlexLink/Plex

```text
K:\
└── plex\
    ├── serials\
    ├── films\
    └── anime\
```

Plex должен читать **только target-каталоги**, а не torrent source.

## Mapping

```text
K:\video\serials → TV      → K:\plex\serials
K:\video\films   → Movie   → K:\plex\films
K:\Anime         → Anime   → K:\plex\anime
```

Source и target находятся на одном NTFS volume `K:`, поэтому hardlinks допустимы.

---

# 3. Интеграция с qBittorrent

## 3.1. Не передавать имя/путь торрента через shell

В qBittorrent использовать external program on completion, но передавать только infohash:

```text
"C:\path\plexlink.exe" process --hash "%I"
```

Причина: torrent name/path являются внешними данными и могут содержать кавычки и другие проблемные символы. Infohash имеет безопасный ограниченный формат.

Все остальные данные утилита получает через qBittorrent Web API.

## 3.2. Используемые qBittorrent API capabilities

Нужны только:

- login;
- torrent info по hash;
- torrent file list по hash.

Получить:

```text
hash
name
content_path
save_path
category
tags
progress/state
file list
```

Для каждого torrent file нужны как минимум:

```text
relative name
size
priority / selected status
progress
```

Не нужно управлять торрентом.

## 3.3. Проверки

`process` должен завершаться без действий, если:

- hash не найден;
- torrent ещё не completed;
- torrent content path не находится ни под одним разрешённым source root;
- нет поддерживаемых media files.

Не сканировать произвольные директории диска. Источником истины должен быть file list конкретного torrent hash.

---

# 4. Определение типа контента

Тип **не угадывать по имени**.

Определять по canonical absolute path:

```text
K:\video\serials → KindTV
K:\video\films   → KindMovie
K:\Anime         → KindAnime
```

Path comparison на Windows должен быть:

- case-insensitive;
- после `filepath.Clean`;
- с защитой от `..\`;
- с проверкой, что путь действительно является descendant root, а не просто имеет похожий string prefix.

Если category qBittorrent позже понадобится, её можно использовать как explicit override, но в MVP source root — главный источник истины.

---

# 5. Какие файлы обрабатывать

MVP media extensions:

```text
.mkv
.mp4
.m4v
.avi
.webm
.ts
.m2ts
```

Игнорировать:

```text
sample
trailer
proof
screens
extras
```

если это очевидно из path/name.

Не обрабатывать в MVP:

- `.nfo`;
- картинки;
- архивы;
- `.exe`;
- `.txt`;
- subtitles.

Sidecar subtitles (`.srt/.ass/.ssa`) добавить отдельной задачей P1 после стабильной работы видео.

---

# 6. Release-name parsing

Основной parser:

```text
github.com/chill-institute/torrentname
```

Использовать его как best-effort parser, не как источник истины.

Нужные поля:

```go
Title
Year
Season
Episode
EpisodeEnd
Part
Resolution
Quality
Codec
Group
Language
Complete
```

## 6.1. Парсить не только torrent name

Для TV/Anime собрать данные из нескольких источников:

1. torrent name;
2. basename `content_path`;
3. имена каждого media file;
4. parent directories файлов.

Причина: torrent folder может быть:

```text
Yellowstone 2 - LostFilm.TV [MP4]
```

а файлы внутри:

```text
Yellowstone.S02E01...
Yellowstone.S02E02...
```

Файлы дают сильное подтверждение, что `2` означает Season 2.

## 6.2. Title candidate

Построить несколько title candidates.

Вес:

```text
parsed torrent name      = 3
parsed content root name = 2
parsed media filenames   = 1 each
```

Нормализовать:

- Unicode trim;
- точки/underscore → spaces;
- collapse whitespace;
- lowercase для comparison;
- punctuation-insensitive comparison.

Не выбрасывать кириллицу.

TMDB умеет находить translated/alternative names, поэтому запрос `Мышь` должен оставаться допустимым кандидатом.

## 6.3. Не писать огромный regexp-parser

Можно добавить небольшой слой project-specific normalization, но он должен быть ограниченным.

Примеры мусора:

```text
LostFilm.TV
Amedia
NewStation
[VARYG]
WEB-DL
1080p
720p
x264
x265
```

Если `torrentname` уже удалил/распознал token, повторно не усложнять.

---

# 7. Metadata provider: TMDB

## 7.1. MVP использует только TMDB

Не добавлять в v0.1:

- TVDB;
- AniList;
- AniDB;
- IMDb scraping;
- ffprobe identity matching.

TMDB должен покрыть фильмы и большинство сериалов/аниме.

Позже provider abstraction позволит добавить fallback.

## 7.2. API operations

TV:

```text
/search/tv
/tv/{id}
/tv/{id}/season/{season}
/tv/{id}/season/{season}/episode/{episode}
```

Movie:

```text
/search/movie
/movie/{id}
```

Использовать TMDB bearer token из environment variable.

Например:

```text
PLEXLINK_TMDB_TOKEN
```

Не писать token в logs.

## 7.3. Canonical language

Для filesystem names использовать canonical metadata с:

```text
language=en-US
```

Это даёт предсказуемые ASCII/English-friendly названия.

Plex сам отображает локализованные русские названия/описания согласно своей library metadata language.

Search query при этом может быть на любом языке, включая русский.

---

# 8. Matching — самая важная часть

Нельзя делать:

```go
candidate := results[0]
```

Первый результат не гарантированно правильный.

Нужен confidence scoring + validation.

## 8.1. TV candidate scoring

Для каждого TMDB candidate:

### Title

```text
normalized exact match with name/original_name/known source title  +60
strong token similarity                                          +0..45
weak/substring match                                             +0..25
```

### Year

Если source parser знает год:

```text
exact year         +20
±1 year            +10
difference > 2     -25
```

Если source не знает год — year ничего не решает.

### Season validation

Если torrent files показывают Season N:

- запросить season N;
- season существует → `+15`;
- season не существует → candidate должен получить сильный penalty или быть исключён.

### Episode validation

Из каждого найденного season взять до 3 representative episode numbers:

- min episode;
- max episode;
- один middle/random deterministic.

Проверить, что episode существует в TMDB.

```text
episode exists     +5 each
episode missing    -30 each
```

Если torrent явно содержит `S02E01`, а candidate вообще не имеет Season 2 — этот candidate не должен победить.

Это специально нужно для случаев вроде:

```text
Yellowstone 2 - LostFilm.TV
```

где существуют разные `Yellowstone`, но file list содержит `S02...`.

## 8.2. Movie scoring

Фильмы проще:

```text
exact normalized title    +60
title similarity          +0..45
exact year                +30
±1 year                   +10
large year mismatch       -40
```

## 8.3. Auto-match threshold

Автоматически принимать candidate только если:

```text
top score >= 80
AND
topScore - secondScore >= 15
```

Значения вынести в config.

Если уверенности недостаточно — **не создавать hardlinks**.

Лучше один unresolved torrent, чем неправильно распознанный сериал в Plex.

---

# 9. Разрешение неоднозначностей

При low confidence:

```text
plexlink process --hash HASH
```

должен:

1. ничего не менять;
2. вывести понятный отчёт;
3. сохранить unresolved report в state directory;
4. вернуть специальный exit code.

Пример:

```text
UNRESOLVED

Torrent:
Killing

Detected:
TV Series

Candidates:
1. The Killing (2007)      tmdb=...
2. The Killing (US) (2011) tmdb=...

No filesystem changes made.
```

CLI:

```text
plexlink inspect --hash HASH
```

показывает parsing + candidates + scoring.

Manual resolution:

```text
plexlink resolve --hash HASH --tmdb-id 12345
```

После explicit resolution:

- проверить, что TMDB entity существует;
- обработать torrent;
- сохранить resolution для этого hash.

Дополнительно:

```text
plexlink resolve --hash HASH --tmdb-id 12345 --remember-alias
```

может добавить alias mapping для будущих сезонов.

Пример `overrides.yaml`:

```yaml
tv:
  killing:
    tmdb_id: 12345
```

Автоматические high-confidence matches в overrides писать не нужно.

---

# 10. Plex path builder

## 10.1. TV Series

Целевая структура:

```text
K:\plex\serials\
└── Show Name (Year) {tmdb-123456}\
    └── Season 02\
        ├── Show Name (Year) - S02E01.ext
        └── Show Name (Year) - S02E02.ext
```

`Season` должен быть именно английским словом.

Если файл содержит несколько эпизодов:

```text
S02E18-E19
```

формировать:

```text
Show Name (Year) - S02E18-E19.ext
```

Specials:

```text
Season 00
Show Name (Year) - S00E01.ext
```

## 10.2. Movies

```text
K:\plex\films\
└── Movie Name (Year) {tmdb-123456}\
    └── Movie Name (Year) {tmdb-123456}.ext
```

## 10.3. Anime

В Plex это TV library.

```text
K:\plex\anime\
└── Anime Name (Year) {tmdb-123456}\
    └── Season 01\
        └── Anime Name (Year) - S01E03.ext
```

---

# 11. Anime policy v0.1

Anime — отдельный source kind, но в MVP resolver всё ещё TMDB TV.

## Explicit SxxExx

Если filename уже содержит:

```text
S01E03
```

обрабатывать как обычный TV.

## Absolute episode number

Пример:

```text
[VARYG] Pluto - 03 [WEB-DL 1080p x264 DDP].mkv
```

Conservative rule:

Если:

- season не указан;
- episode number найден;
- TMDB candidate имеет **ровно один non-special season**;
- episode <= episode_count этого season;

тогда разрешено mapping:

```text
03 → S01E03
```

Иначе:

```text
UNRESOLVED_ANIME_NUMBERING
```

Не пытаться в v0.1 автоматически преобразовывать длинную absolute numbering у многосезонного аниме.

P1/P2:

- AniList title normalization;
- AniDB ED2K exact-file lookup;
- explicit absolute-number mapping.

---

# 12. Hardlink layer

Перед созданием link:

1. source существует;
2. source — regular file;
3. source находится в разрешённом root;
4. target находится внутри соответствующего target root;
5. source и target volume совместимы с hardlink;
6. target directory создан.

Создание:

```go
os.Link(source, target)
```

## Target already exists

```text
same underlying file → NOOP
different file       → CONFLICT
```

Для проверки использовать `os.Stat` + `os.SameFile` там, где это работает на Windows.

Никогда автоматически не удалять conflict target.

---

# 13. Windows filename sanitation

TMDB title может содержать символы, запрещённые Windows.

Нужна функция:

```go
SanitizeWindowsName(string) string
```

Удалять/заменять:

```text
< > : " / \ | ? *
```

Удалять trailing dot/space.

Обработать reserved device names:

```text
CON
PRN
AUX
NUL
COM1..COM9
LPT1..LPT9
```

Санитизация должна быть deterministic и покрыта unit tests.

---

# 14. Configuration

Предпочтительный файл:

```text
config.yaml
```

Пример:

```yaml
qbittorrent:
  url: "http://127.0.0.1:8080"
  username: "admin"
  password_env: "PLEXLINK_QBT_PASSWORD"

tmdb:
  token_env: "PLEXLINK_TMDB_TOKEN"
  language: "en-US"

paths:
  tv_source: "K:\\video\\serials"
  movie_source: "K:\\video\\films"
  anime_source: "K:\\Anime"

  tv_target: "K:\\plex\\serials"
  movie_target: "K:\\plex\\films"
  anime_target: "K:\\plex\\anime"

matching:
  min_score: 80
  min_margin: 15

state:
  directory: "C:\\Users\\Kenny\\AppData\\Local\\PlexLink"
```

Secrets — только через env по умолчанию.

Использовать `gopkg.in/yaml.v3`, без config-framework.

---

# 15. CLI

Не использовать Cobra, если нет объективной необходимости. Для такого проекта достаточно стандартного `flag` + небольшого subcommand dispatcher.

## Commands

### Doctor

```text
plexlink doctor
```

Проверяет:

- config;
- qBittorrent login;
- TMDB token;
- source roots;
- target roots;
- media source/target находятся на подходящем volume;
- реальное создание временного hardlink;
- удаляет только свои temporary doctor files.

### Process

```text
plexlink process --hash <hash>
```

Production path.

### Dry-run

```text
plexlink process --hash <hash> --dry-run
```

Должен выполнить всё кроме `MkdirAll`/`Link`.

Пример output:

```text
Torrent: Yellowstone 2 - LostFilm.TV [MP4]
Kind: TV
Files: 8

Parsed title: Yellowstone
Detected season: 2

MATCH
Yellowstone (2018)
TMDB: 73586
Score: 110
Margin: 42

PLAN
K:\video\serials\...\Yellowstone.S02E01.mp4
  ->
K:\plex\serials\Yellowstone (2018) {tmdb-73586}\Season 02\Yellowstone (2018) - S02E01.mp4

DRY RUN: no filesystem changes
```

### Inspect

```text
plexlink inspect --hash <hash>
```

Показывает:

- qBittorrent metadata;
- parsed titles;
- seasons/episodes;
- TMDB candidates;
- score breakdown.

### Resolve

```text
plexlink resolve --hash HASH --tmdb-id ID
```

Optional:

```text
--remember-alias
```

---

# 16. Exit codes

```text
0  success / already processed
10 ignored (outside roots / no media)
20 unresolved / low confidence
21 unresolved anime numbering
30 target conflict
40 configuration error
41 qBittorrent error
42 TMDB error
50 filesystem/hardlink error
```

Не обязательно использовать именно эти числа, но они должны быть documented и stable.

---

# 17. Logging

Использовать стандартный:

```go
log/slog
```

Никакой отдельной logging library.

Console + rotating log не нужен в MVP.

Достаточно append log file:

```text
%LOCALAPPDATA%\PlexLink\plexlink.log
```

Формат предпочтительно JSONL.

Каждая обработка должна иметь:

```text
torrent_hash
torrent_name
kind
tmdb_id
match_score
source
target
action
error
duration
```

Никогда не логировать qBittorrent password или TMDB token.

---

# 18. State

Не вводить SQLite в MVP без необходимости.

Хранить только:

```text
state/
├── resolutions.yaml
└── unresolved/
    └── <hash>.json
```

Idempotency обеспечивает filesystem, а не база.

Если позже понадобится история/queue/retries — тогда можно добавить SQLite.

---

# 19. Go architecture

Не делать много слоёв ради архитектуры.

Рекомендуемая структура:

```text
plexlink/
├── cmd/
│   └── plexlink/
│       └── main.go
├── internal/
│   ├── app/
│   │   └── processor.go
│   ├── config/
│   ├── qbt/
│   ├── release/
│   ├── tmdb/
│   ├── matcher/
│   ├── plexpath/
│   ├── linker/
│   └── state/
├── testdata/
│   └── releases.json
├── config.example.yaml
├── README.md
└── go.mod
```

Основной orchestration:

```go
type Processor struct {
    Torrents TorrentClient
    Metadata MetadataProvider
    Matcher  Matcher
    Linker   Linker
    Config   Config
}
```

Нужны небольшие interfaces только на внешних границах для тестируемости:

```go
type TorrentClient interface {
    GetTorrent(ctx context.Context, hash string) (Torrent, error)
    GetFiles(ctx context.Context, hash string) ([]TorrentFile, error)
}

type MetadataProvider interface {
    SearchTV(ctx context.Context, query string) ([]TVCandidate, error)
    GetTV(ctx context.Context, id int) (TVShow, error)
    GetSeason(ctx context.Context, id, season int) (Season, error)

    SearchMovie(ctx context.Context, query string) ([]MovieCandidate, error)
    GetMovie(ctx context.Context, id int) (Movie, error)
}
```

Не создавать Repository/Domain Service/UseCase слои, если они ничего не дают.

---

# 20. HTTP clients

И qBittorrent, и TMDB:

- один переиспользуемый `http.Client`;
- timeout;
- context cancellation;
- проверка status code;
- typed errors;
- небольшой retry только для transient:
  - `429`;
  - `502`;
  - `503`;
  - `504`.

Retry:

```text
max 3
exponential backoff
respect Retry-After when present
```

Не retry:

```text
400
401
403
404
```

---

# 21. qBittorrent client implementation

qBittorrent Web API использует session cookie.

Client должен:

1. login;
2. сохранить SID cookie;
3. запросить torrent по hash;
4. запросить torrent files.

Можно использовать `cookiejar`.

Не делать login перед каждым HTTP request внутри одной обработки.

---

# 22. TMDB client implementation

Никакого стороннего TMDB SDK в MVP.

Причина: нужны всего несколько endpoints; собственный маленький typed client проще и прозрачнее.

Structs должны содержать только реально используемые fields.

Например:

```go
type TVSearchResult struct {
    ID            int    `json:"id"`
    Name          string `json:"name"`
    OriginalName  string `json:"original_name"`
    FirstAirDate  string `json:"first_air_date"`
}
```

---

# 23. Реальные regression fixtures

Обязательно добавить реальные noisy names в `testdata/releases.json`.

Минимум:

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

Expected behavior:

### Yellowstone

Если file list подтверждает `S02`, matcher должен предпочесть `Yellowstone (2018)` старым/неподходящим одноимённым кандидатам.

### Counterpart

Папка `Counterpart 2 - LostFilm...` + S02 files должна определяться как:

```text
Counterpart (2017), Season 02
```

### Killing

Если данные не позволяют надёжно отличить Danish 2007 от US 2011:

```text
MUST BE UNRESOLVED
```

Не выбирать первый результат.

### Pantheon

S01 и S02 должны попадать в один:

```text
Pantheon (2022) {tmdb-ID}
```

с разными Season directories.

### Pluto

Для `[VARYG] Pluto - 03 ...` допустим automatic `S01E03` только если после TMDB resolve сериал имеет ровно один regular season и episode 3 существует.

### Cyrillic

`Мышь ...` должен разрешаться через TMDB translated/alternative title в `Mouse (2021)` при достаточной confidence.

---

# 24. Tests

## Unit

Обязательные:

```text
release parsing
title normalization
Windows path containment
Windows filename sanitation
candidate scoring
ambiguity threshold
Plex path builder
existing-target conflict logic
anime single-season absolute mapping
```

## Integration with httptest

Fake qBittorrent server:

- login;
- torrent info;
- file list.

Fake TMDB server:

- search;
- details;
- seasons.

Тестировать полный:

```text
hash → planned hardlinks
```

без реального Internet.

## Filesystem integration

В `t.TempDir()`:

1. создать source file;
2. вызвать linker;
3. проверить target exists;
4. проверить `os.SameFile`;
5. повторить link → NOOP;
6. создать другой target → CONFLICT.

---

# 25. Acceptance criteria для v0.1

Релиз v0.1 готов, когда выполняется всё ниже.

## Functional

- [ ] `plexlink doctor` проходит на Windows.
- [ ] qBittorrent может вызвать CLI по `%I`.
- [ ] TV torrent определяется по source root.
- [ ] Movie torrent определяется по source root.
- [ ] Anime torrent определяется по source root.
- [ ] CLI получает file list только через qBittorrent API.
- [ ] Release parser извлекает основные TV season/episode.
- [ ] TMDB search работает.
- [ ] Matching не использует `results[0]` без scoring.
- [ ] Low-confidence case ничего не изменяет.
- [ ] `--dry-run` ничего не меняет.
- [ ] TV hardlink получает Plex layout.
- [ ] Movie hardlink получает Plex layout.
- [ ] Target includes year.
- [ ] Target show/movie directory содержит `{tmdb-ID}`.
- [ ] Повторная обработка idempotent.
- [ ] Conflict не перезаписывается.
- [ ] Source files никогда не меняются.
- [ ] qBittorrent продолжает раздавать исходные paths.

## Plex

После добавления:

```text
K:\plex\serials
K:\plex\films
K:\plex\anime
```

в Plex и использования:

```text
TV: Plex TV Series
Movies: Plex Movie
```

новый high-confidence torrent должен появляться в Plex с правильным match, после чего Plex сам получает:

- poster;
- description;
- title;
- year;
- episode metadata.

Plex API integration не является частью v0.1. Полагаемся на Plex automatic library update / обычный library scan.

---

# 26. Не делать в v0.1

Чтобы Codex не раздувал scope:

```text
NO Sonarr/Radarr/Prowlarr integration
NO GUI
NO Windows Service
NO embedded web server
NO filesystem watcher
NO SQLite
NO TVDB
NO AniDB
NO AniList
NO ffprobe
NO poster downloading
NO Plex metadata writing
NO NFO generation
NO automatic source cleanup
NO copy fallback
NO symlink fallback
NO torrent search
NO qBittorrent move/rename
```

Hardlink failure = explicit error.

---

# 27. P1 после работающего MVP

Только после v0.1:

1. Sidecar subtitles hardlink.
2. AniList как title-normalization fallback.
3. AniDB ED2K exact match для сложных anime releases.
4. Better anime absolute numbering.
5. Optional Plex library refresh.
6. `process-path` для ручных файлов вне qBittorrent.
7. Background retry queue.
8. Optional SQLite history/cache.
9. Metrics/structured reports.
10. Optional service mode.

---

# 28. План реализации для Codex

## Stage 1 — skeleton

Сделать:

- go module;
- config;
- CLI dispatcher;
- `doctor`;
- logging;
- basic tests.

Не переходить дальше, пока tests green.

## Stage 2 — qBittorrent

Сделать typed client:

- auth;
- torrent by hash;
- file list.

Добавить httptest.

`inspect --hash` пока выводит raw torrent metadata.

## Stage 3 — parser

Подключить:

```text
github.com/chill-institute/torrentname
```

Сделать aggregation из torrent/folder/file names.

Добавить fixtures.

`inspect` показывает parsed representation.

## Stage 4 — TMDB

Typed client + fake integration tests.

`inspect` показывает candidates.

## Stage 5 — matcher

Реализовать deterministic scoring.

Особое внимание:

```text
Yellowstone
Counterpart
Killing
```

`Killing` должен оставаться unresolved при недостатке evidence.

## Stage 6 — path planning

Пока без hardlinks.

```text
process --dry-run
```

должен печатать полный plan.

Прогнать на реальных torrents.

## Stage 7 — hardlinks

Реализовать `linker`.

Добавить idempotency/conflict tests.

Только теперь разрешить production `process`.

## Stage 8 — manual resolution

`inspect`
`resolve`
`--remember-alias`

## Stage 9 — qBittorrent hook

После ручного тестирования:

```text
"C:\path\plexlink.exe" process --hash "%I"
```

Включить в qBittorrent.

---

# 29. Definition of Done

Сценарий должен работать без участия пользователя после выбора torrent:

```text
Пользователь выбирает раздачу
        ↓
qBittorrent скачивает
        ↓
completion hook передаёт infohash
        ↓
PlexLink получает file list
        ↓
определяет Movie / TV / Anime по source root
        ↓
release parser
        ↓
TMDB candidates
        ↓
confidence + season/episode validation
        ↓
high confidence?
   yes             no
    ↓               ↓
hardlinks        unresolved log
    ↓
K:\plex\...
    ↓
Plex
    ↓
poster + description
```

При ambiguous result система должна **остановиться безопасно**, а не угадывать.

---

# 30. Инструкция Codex

Реализуй проект по этой спецификации последовательно, не расширяя scope.

Основные приоритеты:

1. Correctness > automation.
2. Не трогать исходные torrent files.
3. Никакого silent wrong matching.
4. Idempotency.
5. Dry-run до любых filesystem mutations.
6. Простая читаемая Go-архитектура.
7. Standard library там, где это разумно.
8. Маленькие interfaces только на I/O boundaries.
9. Unit/integration tests должны появляться вместе с соответствующим кодом, а не после реализации.
10. Не добавлять функции из раздела P1 до завершения v0.1.

После каждого Stage:

- запусти `go test ./...`;
- исправь ошибки;
- кратко зафиксируй, что реализовано;
- только затем переходи к следующему Stage.

Если спецификация неоднозначна, выбирай **наиболее консервативное поведение**: не создавать hardlink при сомнительном match.
