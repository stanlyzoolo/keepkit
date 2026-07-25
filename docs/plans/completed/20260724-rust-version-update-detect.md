# Rust: installed-detect + updater fallbacks (Вариант A)

## Overview

Чинит две проблемы с Rust-инструментами в keepkit и заодно делает строку `installed:` честной:

1. **`rust` не обновляется.** Трекается как `github.com/rust-lang/rust`, но бинарника `rust` нет — `rustc`/`cargo` стоят из Homebrew (`/opt/homebrew/Cellar/rust/1.96.1`). `updater.Detect` стартует с `exec.LookPath("rust")`, промахивается и сразу отдаёт `ErrUnknownManager` → «no known updater». Правильная команда очевидна: `brew upgrade rust`.
2. **Rust-TUI (пример `inertia`, крейт `inertia-tui`, `~/.cargo/bin/inertia`) показываются `installed: ✕ not found`,** хотя установлены и запускаются по `enter`. Это ratatui-приложение: игнорирует `--version`/`-V`, грузит свой TUI, а запущенное детачнуто (`proc.DetachTTY`) паникует с exit 101. Вывод содержит alt-screen сигнатуру `\x1b[?1049l` и строку `ratatui-0.30.2`. Итог: (а) версия не парсится → not found; (б) anomaly-лог пишется при **каждом** старте; (в) латентный мис-парс — `versionRe` матчит `0.30.2` из `ratatui-0.30.2`, сейчас маскируется ненулевым exit. Версия доступна без запуска бинарника: `cargo install --list` → `inertia-tui v0.1.0:`.
3. **Строка `installed:` схлопывает два разных состояния в «✕ not found»:** (а) установлена, но версию не определить; (б) не установлена вообще. Различаем.

**Решение — Вариант A:** автоматические fallback'и, симметричные существующему `brewDirVersion` (никакой ручной настройки), плюс различение present/not-installed. `version` и `updater` остаются нижними листами графа, друг друга и `model` не импортируют — маленькие куски дублируем, а не абстрагируем (философия кодовой базы: per-package чистые ядра + свои тест-сиды, ср. `shellCommand`/`browserCommand`/`planFor`).

## Context (from discovery)

- **Проект:** Go TUI (Bubble Tea), трекер CLI-инструментов. Тесты — table-driven, `go test -race ./...`, golangci-lint v2.
- **Файлы/области:**
  - `internal/version/detect.go` — `InstalledVersion`, `versionRe`; образцы `brewDirVersion`/`brewPrefix`/`testBrewPrefix` в `internal/version/brew.go`.
  - `internal/updater/updater.go` — `Detect`, `detectFromPath`, `cargoCrateFromList`, `autoPlan`, `runProbe`, сид `testHomeDir`.
  - `internal/model/model.go` — `VersionInfo`, `installedMsg`, `case installedMsg` (~427).
  - `internal/model/commands.go:159` — `fetchInstalledCmd` (единственный продакшн-вызов `InstalledVersion`).
  - `internal/model/render.go` (~1095) — switch рендера `installed:`.
  - `internal/model/textutil.go:196` — `isTUITakeover` (образец для дубля).
  - `internal/ui/styles.go` — стили, `ColorGreen` (#6AAF6A), `DangerStyle`.
- **Ripple от смены сигнатуры `InstalledVersion`:** продакшн — `commands.go:159`; тесты — `detect_test.go` (строки 87, 104, 123, 145, 162), `brew_test.go` (150, 154, 157).
- **Паттерны:** «узнать версию не запуская бинарник» уже есть ровно в одном месте — `brewDirVersion`. Добавляем симметрично: cargo-list в `version`, brew-by-name в `updater`.

## Development Approach

- **testing approach:** Regular (реализация, затем тесты в той же задаче) — под table-driven стиль пакетов.
- каждую задачу доводить до конца перед следующей; маленькие сфокусированные изменения.
- **CRITICAL: каждая задача включает новые/обновлённые тесты** для своего кода (успех + ошибки/крайние случаи).
- **CRITICAL: все тесты зелёные перед следующей задачей.**
- **CRITICAL: держать `go build ./...` зелёным на каждой задаче** — смена сигнатуры `InstalledVersion` компилируется только вместе со всеми потребителями, поэтому сигнатура и её plumbing в `model` объединены в одну задачу (Task 1).
- **CRITICAL: обновлять этот файл при изменении скоупа.**
- прогонять `go test -race ./...` после каждого изменения; сохранять обратную совместимость `meta.yaml`.

## Testing Strategy

- **unit tests:** обязательны в каждой задаче.
  - чистые ядра (`cargoVersionFromList`, `brewNamePlanAt`) — table-driven.
  - сиды: `testBrewPrefix` (`updater`, дубль), `testHomeDir` (уже есть).
  - рендер — прямая проверка строки карточки по 4 состояниям.
- **e2e:** в проекте нет UI-e2e (только `demo/*.tape` для GIF-ов, вне области). Ручной smoke — в Post-Completion.

## Progress Tracking

- `[x]` сразу по завершении; ➕ для новых задач; ⚠️ для блокеров; синхронизировать план с фактом.

## Solution Overview

- **`version`:** `InstalledVersion` возвращает `(ver, present)`. Порядок источников версии: `--version`/`-V` → `brewDirVersion` → **новый** `cargoListVersion` (только когда бинарник есть, но версии нет). Гард `isTUITakeover` отсекает alt-screen-вывод до `versionRe` — убирает мис-парс и anomaly-лог для TUI-приложений. `present` = бинарник в PATH (`LookPath`) или версия найдена fallback'ом.
- **`updater`:** на промахе `LookPath` в `Detect`, до `ErrUnknownManager`, пробуем brew-by-name (`Cellar/<name>`/`Caskroom/<name>` существует → `brew upgrade <name>`). Самовалидирующаяся ветка (срабатывает только при name==формула).
- **`model`/`ui`:** `installed:` — 4 состояния; `✓ no version` (`ui.OkStyle`, зелёный) для present-без-версии, `✕ not installed` (`DangerStyle`) для отсутствия.

## Technical Details

- `isTUITakeover(out []byte) bool` = `bytes.Contains(out, []byte("\x1b[?1049"))` — дубль из `model/textutil.go` (не импортим `model` в `version`).
- `cargoVersionFromList(list, binName string) string`: идём по строкам; header (не начинается с пробела/таба) вида `<crate> vX.Y.Z:` — запоминаем версию через **`versionRe.FindStringSubmatch(line)[1]`** (группа `(\d+\.\d+[\d.]*)` **без** ведущего `v` — `FindString` вернул бы `v0.1.0`, а installed-строки в проекте без `v`: brew-dir → `1.96.1`, `--version` → `26.5.6`); indented-строка = имя бинарника; `trimmed == binName` → версия запомненного header'а. `inertia` в `inertia-tui v0.1.0:` → `0.1.0`.
- `cargoListVersion(binName string) string`: **у `version` нет `runProbe`** (он в `updater`), поэтому инлайним свой прогон с собственным таймаутом: `LookPath("cargo")` (при промахе `""` мгновенно) → `context.WithTimeout` (мирроринг 2s из `InstalledVersion`, чтобы большой список крейтов не стопорил горутину `fetchInstalledCmd`) + `exec.CommandContext("cargo","install","--list")` + `proc.DetachTTY` → `cargoVersionFromList`.
- `brewPrefix()` в `updater` (дубль ~12 строк из `version/brew.go`): `HOMEBREW_PREFIX` env, иначе первый существующий из `/opt/homebrew`, `/usr/local`, `/home/linuxbrew/.linuxbrew`; сид `var testBrewPrefix string`.
- `brewNamePlanAt(name, prefix string) (Plan, bool)`: `prefix==""` || `name==""` || `name` содержит `/`/`\` → `false` (traversal-гард, зеркало `brewDirVersion`); `os.Stat`+`IsDir` для `<prefix>/Cellar/<name>` и `<prefix>/Caskroom/<name>` → `autoPlan("brew", []string{"brew","upgrade",name})`.
- Рендер (switch в порядке): `installed!=""` → `installed:  <v>`; `InstalledKnown && ""  && InstalledPresent` → `installed: ` + `OkStyle.Render("✓")` + ` no version`; `InstalledKnown && "" && !InstalledPresent` → `installed: ` + `DangerStyle.Render("✕")` + ` not installed`; default → `installed: detecting…`. **Правим только две средние ветви и default** — ветку `installed != ""` (сохраняет glyph U+F412 + версию, render.go:1097) не трогаем.
- **Не трогаем:** `hasUpdate`/`↑` (нужны обе версии), `needsInstalled` (завязан на `InstalledKnown`).

## What Goes Where

- **Implementation Steps** (`[ ]`): код в `internal/version`, `internal/updater`, `internal/model`, `internal/ui`, их тесты, обновление CLAUDE.md.
- **Post-Completion** (без чекбоксов): ручной smoke в живом TUI; caveat про источник данных GitHub Releases для rust.

## Implementation Steps

### Task 1: `version.InstalledVersion` — presence + cargo/TUI fallbacks (+ model plumbing)

Смена сигнатуры компилируется только вместе с потребителем, поэтому `version/detect.go` и минимальный plumbing в `model` — одна задача. Рендер (4 состояния) вынесен в Task 2 — он на компиляцию не влияет.

**Files:**
- Modify: `internal/version/detect.go`
- Modify: `internal/version/detect_test.go`
- Modify: `internal/version/brew_test.go`
- Modify: `internal/model/model.go`
- Modify: `internal/model/commands.go`

- [x] `detect.go`: сменить сигнатуру `InstalledVersion(t loader.Tool) string` → `(ver string, present bool)`; завести `binaryExists bool`, ставить `true` на успешном `exec.LookPath(args[0])` (на промахе — не трогать)
- [x] `detect.go`: добавить дубль `isTUITakeover(out []byte) bool` (`bytes.Contains(out, []byte("\x1b[?1049"))`, import `"bytes"`); сразу после `cmd.CombinedOutput()` — если `isTUITakeover(out)`, сбросить capture и `break` из цикла, **не** добавляя reason
  - ➕ уточнение скоупа: на takeover сбрасываем **и накопленные** `reasons` (`reasons = nil`), а не только текущий capture. Иначе тул, у которого `--version` падает не-нулём, а `-V` уходит в TUI, всё равно писал бы anomaly-лог на каждом старте — ровно тот сигнал logx, который задача чинит. Takeover — это классифицированное поведение, а не аномалия
- [x] `detect.go`: добавить `cargoVersionFromList(list, binName string) string` — чистый парсер header/indented, версию берёт через `versionRe.FindStringSubmatch(line)[1]` (**без** ведущего `v`, чтобы `installed:` совпадал с brew/`--version` формой)
- [x] `detect.go`: добавить `cargoListVersion(binName string) string` — `LookPath("cargo")` (промах → `""`) → **свой** `context.WithTimeout` (2s, как в `InstalledVersion`; `version` не имеет `runProbe`) + `exec.CommandContext` + `proc.DetachTTY` → `cargoVersionFromList`
- [x] `detect.go`: порядок fallback `--version`/`-V` → `brewDirVersion(t.Name)` → `cargoListVersion(t.Name)` (вызывать **только** при `binaryExists`); вернуть `(ver, present)`, где `present = binaryExists || ver!=""`
- [x] `model/model.go`: добавить `present bool` в `installedMsg`; `InstalledPresent bool` в `VersionInfo` (коммент почему); в `case installedMsg` — `info.InstalledPresent = msg.present`
- [x] `model/commands.go`: `fetchInstalledCmd` → `ver, present := version.InstalledVersion(t)`; прокинуть оба в `installedMsg`
- [x] обновить существующие тесты под 2-значную сигнатуру: `detect_test.go` (строки ~87/104/123/145/162), `brew_test.go` (~150/154/157)
- [x] написать тесты: `cargoVersionFromList` (несколько крейтов, бинарник во втором блоке, `inertia-tui v0.1.0:` → `0.1.0`, промах → `""`); дубль `isTUITakeover` (сырой `\x1b[?1049` → true)
- [x] написать тесты: present-но-без-версии (LookPath есть, версии нет → `present==true, ver==""`) и not-present (`missingtool` → `present==false`); проверить, что TUI-takeover-вывод **не** пишет anomaly-лог и не парсит `0.30.2`
- [x] `go build ./...` + `go test -race ./internal/version/... ./internal/model/...` — зелёные перед Task 2

### Task 2: Рендер `installed:` — 4 состояния + `ui.OkStyle`

**Files:**
- Modify: `internal/ui/styles.go`
- Modify: `internal/model/render.go`
- Modify: `internal/model/render_test.go`

- [x] `ui/styles.go`: добавить `OkStyle = lipgloss.NewStyle().Foreground(ColorGreen)` (не переиспользовать `UpdateAvailableStyle`)
- [x] `render.go` (~1095): расширить switch до 4 ветвей — версия / `✓ no version` (`OkStyle`) / `✕ not installed` (`DangerStyle`) / `detecting…`
- [x] **обновить существующий подтест** `render_test.go:2558-2566` «detection reported empty: not found»: `VersionInfo{Latest:"v2.0.0", InstalledKnown:true}` теперь рендерит `✕ not installed` — поменять assert `installed: ✕ not found` → `installed: ✕ not installed`; поправить doc-коммент `TestRenderCardInstalledLatest` (строка ~2517, `"✕ not found" once it reported empty`)
- [x] добавить подтест `present-but-no-version`: `VersionInfo{Latest:"v2.0.0", InstalledKnown:true, InstalledPresent:true}` → assert `installed: ✓ no version`
- [x] (подтест `detecting…` на строке ~2568 остаётся как есть — `InstalledKnown:false`); его негативный assert перевели с мёртвой строки `"not found"` на актуальную `"not installed"`
- [x] `go test -race ./internal/model/...` — зелёные перед Task 3

### Task 3: `updater` — brew-by-name для бинарников не в PATH

**Files:**
- Modify: `internal/updater/updater.go`
- Modify: `internal/updater/updater_test.go`

- [x] `updater.go`: добавить `brewPrefix()` + сид `var testBrewPrefix string` (дубль из `version/brew.go`)
- [x] `updater.go`: добавить чистое ядро `brewNamePlanAt(name, prefix string) (Plan, bool)` (traversal-гард на `/`/`\`; `Cellar/<name>` и `Caskroom/<name>` через `os.Stat`+`IsDir`; хит → `autoPlan("brew", ["brew","upgrade",name])`) + обёртку `brewNamePlan(name) (Plan, bool)`
- [x] `updater.go`: в `Detect`, в ветке промаха `exec.LookPath(t.Name)` — до возврата `ErrUnknownManager` вставить `if plan, ok := brewNamePlan(t.Name); ok { return plan, nil }`
- [x] написать тесты: `brewNamePlanAt` table (temp-prefix с `Cellar/rust/1.96.1` → `brew upgrade rust`; нет каталога → `ok=false`; имя с `/`/`\` → `false`)
- [x] написать тест `Detect` через `testBrewPrefix` (name без бинарника, но с `Cellar/<name>` → brew-план); заодно закрепили, что `update_cmd` по-прежнему выигрывает у новой ветки
- [x] ➕ `TestDetectMissingBinary` изолирован пустым `testBrewPrefix` — иначе исход зависел бы от того, что стоит на машине через brew
- [x] `go test -race ./internal/updater/...` — зелёные перед Task 4

### Task 4: Verify acceptance criteria

- [x] сверить, что все пункты Overview реализованы (rust → `brew upgrade rust`; inertia → `installed: 0.1.0`; не-установленный тул → `✕ not installed`; present-без-версии → `✓ no version`)
  - проверено временными probe-тестами против **живой машины** (созданы, прогнаны, удалены). `version.InstalledVersion`: `inertia` → `("0.1.0", true)`, `rust` → `("1.96.1", true)`, `definitely-missing-xyz` → `("", false)`, `git` → `("2.50.1", true)`; лог по итогам всех четырёх — **пустой**. `updater.Detect`: `rust` → `brew upgrade rust`, `inertia` → `cargo install inertia-tui`, отсутствующий тул → `ErrUnknownManager`
  - ⚠️ нюанс харнесса: `TestMain` пакета `version` пинит `testBrewPrefix` в пустой temp-каталог, поэтому probe изнутри пакета видит `rust` как not-present, пока префикс не вернуть на `/opt/homebrew`. На продакшн-путь не влияет, но любой будущий probe об это споткнётся
- [x] сверить крайние случаи (нет cargo → мгновенный short-circuit; TUI-takeover → нет anomaly-лога и мис-парса; traversal-гард)
- [x] прогнать полный набор: `go build ./... && go vet ./... && go test -race ./...`
- [x] прогнать lint: `golangci-lint run` (проверить отсутствие `unused` на новых хелперах) — `0 issues`
- [x] (skill `preflight` покрывает build/vet/test-race/lint одним прогоном)

### Task 5: Update documentation

- [x] обновить `CLAUDE.md` (skill `docs-sync`): в описании version-fallback'ов добавить cargo-list рядом с brew; в цепочке `updater` — brew-by-name на промахе `LookPath`; отметить 4-е состояние `installed:` и новую сигнатуру `InstalledVersion(t) (string, bool)`
  - плюс: `installedMsg{…, present}` в async-split, `isTUITakeover` в секции sandboxing, оговорка про `testBrewPrefix` в двух пакетах, logx-сайт `InstalledVersion`
- [x] ➕ обновить `ARCHITECTURE.md` (skill `docs-sync` требует все три документа): таблица пакетов, «пять источников данных» (installed), секция `Updating a tool (u)`, список тест-сидов. Граф импортов не менялся — новых внутренних зависимостей нет, mermaid трогать не нужно
- [x] обновить README.md, если затронуты видимые фичи (оказалось — нужно): 4 состояния `installed:` в разделе `Panel [2] Brief` + brew-by-name в `Updating tools`
- [x] переместить этот план в `docs/plans/completed/`

## Post-Completion

*Только ручные/внешние действия — без чекбоксов.*

**Ручной smoke в живом TUI** (`go run .`):
- `rust`: карточка `[2]`, `[u]` — теперь либо запускает `brew upgrade rust`, либо честно «no update available» (см. caveat ниже), но **не** «no known updater».
- `inertia`: `installed:  0.1.0` без «not found», в `logs/` нет нового файла после старта.
- любой не-установленный трекнутый тул: `installed: ✕ not installed`.
- проверить, что `logs/` не пополняется на чистом старте (сигнал logx «лог = что-то сломалось» цел).

**Caveat (скоуп не расширять):**
- Фикс `updater` убирает тупик «no known updater», но *предложит* ли keepkit апдейт rust (маркер `↑` + активный `[u]`) зависит от того, публикует ли `github.com/rust-lang/rust` GitHub **Releases** и есть ли тег новее установленного `1.96.1`. Если релизов нет или версия совпадает — `[u]` скажет «no update available». Это про источник данных, Вариантом A не покрывается.
- Присутствие определяется по `LookPath`, а не по наличию man/help (они в `model`; тянуть в `version` — инверсия графа). Кейс «man есть, бинарника в PATH нет» не покрываем (YAGNI).
- Максимум один лишний `cargo install --list` для редкого «установлен, непробуем, не brew и не cargo» тула; при отсутствии cargo `LookPath` закорачивает мгновенно.
