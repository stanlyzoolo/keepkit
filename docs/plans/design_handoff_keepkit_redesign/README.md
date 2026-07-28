# Handoff: keepkit TUI redesign

## Overview
Редизайн трёхпанельного экрана keepkit (bubbletea + lipgloss): сокращённая палитра с ролями, информативный список инструментов, перепланированная карточка brief (метрики-полоса, компактный changelog), очищенный readme, статусбар без постоянного API-гейджа. Цель — быстрее считываемая иерархия: «что устарело и что с этим сделать» видно за один взгляд.

## About the Design Files
Файл `Keepkit Redesign.dc.html` — **дизайн-референс в HTML**, а не код для копирования. Задача — воспроизвести этот дизайн в существующем Go-коде keepkit (charmbracelet/bubbletea + lipgloss), следуя паттернам репозитория stanlyzoolo/keepkit: палитра и стили в `internal/ui/styles.go`, рендер в `internal/model/render.go`, очистка readme в `internal/model/readme_clean.go`. HTML использует px и CSS-flex; в TUI это транслируется в символьные колонки, lipgloss.JoinHorizontal/JoinVertical и выравнивание через lipgloss.Width.

## Fidelity
**High-fidelity** по цветам, иерархии, составу и порядку элементов, выравниваниям. Размеры в px — ориентир пропорций (панели ≈ 20% / 46% / 34% ширины терминала); межстрочные интервалы и скругления рамок остаются терминальными (RoundedBorder, одна строка = один row).

## Design Tokens

### Роли цветов (тема = ровно этот список)
| Роль | Hex | Использование |
|---|---|---|
| accent (ColorPrimary) | #DA7756 | фокус: рамка активной панели, ⏺-курсор, имя выбранного tool, ВСЕ хоткеи |
| signal (ColorOrange) | #E5A040 | только «требует действия»: ↑ у устаревших версий, latest при апдейте, счётчик updates, status trying |
| ok (ColorGreen) | #6AAF6A | здоровое состояние: ● active, метка feat, версия самого keepkit «0.8.1 •» |
| danger (ColorDanger) | #D06060 | сломанное: битый SKILL.md, критичный rate-limit |
| link (ColorMeta) | #5F93B8 | ТОЛЬКО URL/ссылки (release notes ↗). Прежние применения #5588AA (мета, хинты) → dim |
| emphasis (ColorEmphasis, новый) | #F2F3F5 | ярчайший текст: имя tool в карточке, значения метрик, болд-лиды в readme, заголовки секций |
| text (ColorText) | #A8ADB8 | основной текст (было #E8E8E8 — теперь на ступень тише) |
| dim (ColorDim) | #767C88 | лейблы, SHA, счётчики, вторичные хинты |
| border (ColorBorder) | #454B57 | рамки панелей, разделители, вертикальные линии метрик |
| surface (ColorSurface, новый) | #343945 | фон выбранной строки, блока метрик, код-плашек. Фон терминала #2E323B |

### Удалить из styles.go
- ColorCategory #E8A87C и ColorKey #C8A97E — их роли закрываются весом (Bold) + emphasis/dim.
  SectionLabelStyle, HelpSectionStyle → Bold + ColorText/ColorEmphasis; SearchMatchStyle → Bold + ColorPrimary.
- ColorMuted #AAAAAA — сливается с ColorText → A8ADB8.
- ColorOrangeDim #7A5A1E остаётся (трек гейджа).

### Типографская иерархия (в TUI — вес и цвет вместо кегля)
- Имя tool в карточке: Bold + emphasis (в HTML 19px — единственный «крупный» элемент экрана)
- Заголовок активной панели: Bold + accent + префикс ▸; неактивных — обычный dim
- Лейблы метрик (INSTALLED/LATEST/…): dim, UPPERCASE, разрядка
- Ровно 3 ярчайшие точки экрана: выбранный tool в списке, имя tool в карточке, latest при апдейте

## Screens / Views

### Панель [1] tools (≈20% ширины, фокус по умолчанию)
- Заголовок на рамке: `▸ [1] tools 27 3↑` — «27» dim, «3↑» signal. У неактивных панелей ▸ нет.
- Строка: [маркер 1 колонка][имя, flex][версия, правое выравнивание]. Имя text; версия dim; у устаревших версия+↑ = signal. Сам keepkit: версия ok-зелёная с «•».
- Выбранная строка: ⏺ accent, имя Bold accent, фон surface НА ПОЛНУЮ ширину панели (в HTML margin: 0 -12px).
- Группы: divider `label ────` — label dim с letter-spacing, линия border. Группа **updates всегда первая**, дальше тег-группы (cli, tui, …).
- Футер панели (прижат вниз, отделён линией): `/ filter · g group by tag` — клавиши accent, текст dim, 12.5px.

### Панель [2] brief (≈46%)
Сверху вниз, gap 16px:
1. **Заголовок**: `lazyskills` Bold emphasis + рядом `alvinunreal/lazyskills` dim (repo больше не строка [info]). Ниже тэглайн text.
2. **Полоса метрик** — фон surface, radius 4px, padding 12×14, 4 равные колонки с вертикальными разделителями border:
   INSTALLED `v0.3.2` emphasis · LATEST `v1.0.2 ↑` Bold signal + дата dim · MAINTENANCE `● active` ok · STARS `219` emphasis. Лейблы dim 12px uppercase.
3. **Мета одной строкой** (flex, wrap): `lang go 99% · shell 1%` (доли <1% не показывать) · `status ◌ trying` signal · `tags — # add` · `note — e write`. Лейблы dim, клавиши accent. Пустые note/tags НЕ занимают отдельных строк.
4. **Changelog**: шапка `changelog` Bold text + `v0.3.2 → v1.0.2 · 12 commits` dim + линия + ссылка `release notes ↗` link-цветом.
   Строка коммита: [тип 42px, правое выравнивание: feat=ok, fix=text][сообщение flex, text][SHA 7 символов, dim]. Полный SHA не показывается никогда. Хвост: `+2 more · j/k scroll` dim.
5. **Футер**: `enter update to v1.0.2 · r refresh` — контекстное действие живёт в панели, клавиши accent.

### Панель [3] readme (≈34%)
- Заголовок: `[2]-стиль` + подсказка режимов `· h help · m man` цветом border.
- **h1 и слоган-первый-абзац вырезаются** (`readme_clean.go`) — они дублируют карточку. Начало сразу с текста Overview.
- Секции: `Features`, `Install` — Bold emphasis + линия border до края (замена ##).
- Features: строки «Bold-лид emphasis — описание text». Битые артефакты (SKILL.md) — danger.
- Install: команды на плашках surface, radius 3px, padding 8×12, текст #E8E8E8.
- Футер: `readme · 1/3` слева, `d/u page` справа, dim.

### Статусбар (глобальный)
- Слева: только глобальные ключи: `enter run · / search · t track · u untrack · R rename · ? keys · q quit` — клавиша accent, слово text. Rename перевешен на **R**, теги на **#** (конфликт t устранён).
- Справа: `3 updates ↑` signal + `U update all` dim. **API-гейдж скрыт по умолчанию**; показывается левее updates только при остатке <500 или использовании >50%: `api ▮▮▮▮░░░░░░ 1712/5000` (fill #E5A040, track #7A5A1E). Никакого Title Case — всё lowercase.

### Оверлей выбора темы (фича после THEMING-PREWORK.md)
См. `screenshots/theme-overlay.png`. Рисуется существующей инфраструктурой (`internal/ui/overlay.go`: рамка RoundedBorder цветом accent, фон затемняется OverlayDimStyle-репейнтом).
- **Ширина** ~62 колонки, по центру. Титульная строка: `themes` Bold + путь конфига `~/.config/keepkit/theme` dim + справа `esc cancel` (close-hint в титуле, без отдельной нижней строки — ваш паттерн из hotkeys-оверлея).
- **Строка темы**: [маркер 1 кол][имя, flex][тег курсивом dim][5 сватчей]. Сватчи — цвета ролей темы в фиксированном порядке: accent · signal · ok · danger · text (в терминале — ●●●●● фг-цветом). Тег `active` у сохранённой темы, `previewing` у превьюируемой.
- **Выбранная строка**: ⏺ + Bold имя цветом accent, фон surface.
- **Футер**: `j/k move · space preview · enter apply` + справа курсивом `preview reverts on esc`.
- **Live-превью**: `space` применяет Theme ко ВСЕМУ кадру немедленно — фону, панелям и самому оверлею (на скриншоте оверлей уже в gruvbox). `enter` персистит выбор в configdir, `esc` откатывает к сохранённой теме.
- **Стартовый набор тем**: keepkit (default), gruvbox, catppuccin mocha, nord, monochrome — каждая = 10 значений Theme-структуры.

### Оверлей горячих клавиш
См. `screenshots/keys-overlay.png`. Эволюция существующего hotkeys-оверлея (`docs/plans/20260720-hotkeys-overlay-vim-scroll.md`) под новую палитру — структура сохранена:
- **Титульная строка**: `keys` Bold emphasis + `keepkit <version>` dim + справа `esc close` (close-hint в титуле, отдельной нижней строки нет — критично для 80×24).
- **Две колонки** через JoinHorizontal; каждая группа названа по панели/области: `global`, `[1] tools`, `[2] brief`, `[3] readme`, `status`. Заголовок группы: Bold + ColorText (НЕ отдельный цвет — SectionLabelStyle в старом смысле упразднён).
- **Строка**: клавиша в колонке фиксированной ширины (выравнивание по самой длинной, ~7 ячеек), цвет accent; описание text. Единственный цветной акцент в описаниях — живой счётчик у `U update all` (`3 ↑` signal).
- **Раскладка по редизайну**: `#` edit tags, `R` rename, `U` update all, `T` themes, `L` api details, `?` — этот оверлей. Списки клавиш в макете примерные — сверить с реальным кеймапом (`internal/model/mode.go`).

## Interactions & Behavior
- Фокус панели: рамка accent + ▸ и Bold в заголовке (второй, неколорный признак — обязательно).
- ⏺-курсор при потере фокуса списком: dim (как сейчас), фон surface остаётся.
- Клавиши: # edit tags, e edit note, U update all, enter в контексте списка = run, в карточке с апдейтом = update.
- Гейдж: пороги остатка <500 или >50% использования; при <100 остатка — danger.

## State Management
Новое состояние минимально: счётчик доступных обновлений (для заголовка [1] и правого угла статусбара), признак «гейдж видим» (выводится из rate-limit ответа). Группа updates — представление, вычисляется из существующих данных версий.

## Порядок внедрения (рекомендуемый)
1. `internal/ui/styles.go` — палитра и стили (мгновенный эффект на весь UI)
2. `internal/model/render.go` — buildToolRows: колонка версий, группа updates, счётчики в заголовке
3. `internal/model/render.go` — карточка [2]: полоса метрик, мета-строка, changelog-строки
4. `internal/model/readme_clean.go` — вырезание h1+слогана
5. `renderHintsBar` — условный гейдж, блок updates, раскладка клавиш (R, #)

Опционально до шага 1: рефакторинг стилей из глобальных var в Theme-структуру (`NewStyles(Theme)`), если планируется смена тем.

## Assets
Нет. Шрифт макета — JetBrains Mono (в TUI шрифт задаёт терминал пользователя).

## Files
- `Keepkit Redesign.dc.html` — макет: полоса палитры + полный экран. Открывается в браузере.
- `screenshots/full.png` — палитра + экран целиком (1560px)
- `screenshots/terminal.png` — только экран приложения (эталон для сверки)
- `screenshots/theme-overlay.png` — оверлей выбора темы в момент live-превью gruvbox
- `screenshots/keys-overlay.png` — оверлей горячих клавиш
- `THEMING-PREWORK.md` — обязательный рефакторинг ПЕРЕД оверлеем тем (Theme-структура, NewStyles)
