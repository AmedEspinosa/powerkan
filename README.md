# Powerkan

Powerkan is a local-first, keyboard-centric terminal Kanban application for managing developer tickets and active sprints. It is built with Go, Bubble Tea, Lip Gloss, and SQLite.

The current repo state is an MVP-focused beta:

- Service-backed TUI with Board, Tickets, Ticket Detail, and Export screens
- SQLite storage with automated migrations
- Ticket movement across board columns with persistence
- Inline table editing and full ticket detail editing
- Ticket export to Markdown or CSV
- Sprint-end webhook delivery for cron-based reporting

## Current MVP Scope

### TUI Routes

- `1` Board
- `2` Sprints
- `3` Tickets
- `4` Export

`Sprints` is still a placeholder route in the TUI. Ticket Detail is a subview opened from Board or Tickets with `Enter`.

### Board View

- Active sprint board with four columns:
  - `Not Started`
  - `In Progress`
  - `Under Review`
  - `Completed`
- Read-only sprint stats panel
- Read-only selected ticket details panel
- Local board search over ticket title + description
- Local board blocked-only filter
- Ticket movement across statuses with `H` and `L`

### Tickets View

- Tabular ticket list
- Row and column navigation
- Inline editing for editable cells
- `Enter` opens the focused ticket in Ticket Detail

### Ticket Detail View

- Field-by-field ticket editing
- Comment creation
- Back navigation to the previous route

### Export View

- Exports the currently selected/open ticket
- Formats:
  - Markdown
  - CSV

## Keyboard Model

Powerkan uses a strict split between `Normal` mode and `Insert` mode.

- In `Normal` mode, single-key shortcuts trigger navigation and actions.
- In `Insert` mode, typed characters go into the active field instead of triggering shortcuts.
- `Esc` cancels the active edit and returns to `Normal` mode.
- `Enter` saves the active edit and returns to `Normal` mode.

## Keybindings

### Global

- `1` Board
- `2` Sprints
- `3` Tickets
- `4` Export
- `q` or `Ctrl+C` quit

### Board

- `h` / `l` move focus between columns
- `j` / `k` move focus within the current column
- `H` move the selected ticket left
- `L` move the selected ticket right
- `s` edit board search
- `f` toggle blocked-only filter
- `Enter` open Ticket Detail

### Tickets

- `h` / `l` move between columns
- `j` / `k` move between rows
- `i` or `e` edit the focused cell
- `Enter` open Ticket Detail

### Ticket Detail

- `j` / `k` move between fields
- `i` or `e` edit the focused field
- `Esc` cancel edit or return to previous screen
- `Enter` save the active field edit

### Export

- `h` / `l` switch export format
- `Enter` export the current ticket

## Installation and Setup

Run from source for now.

1. Clone the repository.

```bash
git clone https://github.com/yourusername/powerkan.git
cd powerkan
```

2. Create the application config.

```bash
mkdir -p ~/Library/Application\ Support/powerkan/
cp config.example.yaml ~/Library/Application\ Support/powerkan/config.yaml
```

3. Edit `config.yaml` as needed.

4. Run the application.

```bash
go run ./cmd/powerkan
```

## CLI Usage

### Launch TUI

```bash
powerkan
```

### Export a Ticket

```bash
powerkan export ticket --id <TICKET_ID> --format md
powerkan export ticket --id <TICKET_ID> --format csv
```

Optional output path:

```bash
powerkan export ticket --id <TICKET_ID> --format md --out /tmp/ticket.md
```

### Post Sprint-End Webhooks

```bash
powerkan webhook sprint-end
```

Specific sprint:

```bash
powerkan webhook sprint-end --sprint 12
```

Bypass idempotency:

```bash
powerkan webhook sprint-end --force
```

## Storage

- SQLite database and app files live under `~/Library/Application Support/powerkan/`
- Schema migrations run automatically at startup
- Ticket exports default to the configured exports directory

## Known MVP Gaps

- `Sprints` route is still a placeholder
- Board filter UI is limited to blocked-only toggle
- Board search/filter state is local to the board and does not affect the tickets table
- Calendar widget is still a placeholder
- No GitHub sync yet
- No advanced table virtualization yet

## Development

Run the test suite:

```bash
go test ./...
```
