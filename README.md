# Powerkan ⚡️

> 🚧 **Work in Progress:** This is a personal project in early active development, serving as a hands-on exploration of Go and terminal UI architecture. Expect breaking changes, missing features, and rough edges!

Powerkan (Power User + Kanban) is a local-first, keyboard-centric terminal (TUI) Kanban application. It is designed for developers who want a fast, zero-distraction environment to manage their sprints and tickets entirely from the command line, without the overhead of heavy web-based trackers.

Built with **Go**, **Bubble Tea**, and **SQLite**.

## ✨ Features

- **Terminal-Native TUI:** A beautiful, responsive interface built with Charmbracelet's Bubble Tea and Lip Gloss.
- **Keyboard-Centric:** Fully navigable without a mouse. Uses standard `h/j/k/l` bindings for movement and intuitive shortcuts for actions.
- **Local-First & Fast:** Data is stored locally in SQLite (`~/Library/Application Support/powerkan/`). No cloud syncing, no latency, no internet required.
- **Sprint & Backlog Management:** \* Dedicated Active Sprint Board with 4 core columns (Not Started, In Progress, Under Review, Done).
  - Auto-calculated KPIs (points completed, points per day, days remaining).
  - Robust Backlog view with filtering by Epic/Parent and Status.
- **Developer-Friendly Integrations:**
  - Export individual tickets to Markdown or CSV.
  - Cron-triggered webhooks to automatically report end-of-sprint metrics to external services (Make, Notion, etc.).
- **Smart Ticket IDs:** Auto-generated structured IDs (e.g., `APP-FEA-2604130915`) based on Epic, Type, and Timestamp.

## 🚀 Installation & Setup

_(Instructions will be updated as binary distribution is finalized. For now, run from source.)_

1. **Clone the repository:**

   ```bash
   git clone [https://github.com/yourusername/powerkan.git](https://github.com/yourusername/powerkan.git)
   cd powerkan
   ```

2. **Set up your configuration:**
   Powerkan requires a `config.yaml` file for webhook endpoints and app settings. This file is ignored by Git to protect your secrets.

   ```bash
   mkdir -p ~/Library/Application\ Support/powerkan/
   cp config.example.yaml ~/Library/Application\ Support/powerkan/config.yaml
   ```

   _Edit `config.yaml` with your preferred settings._

3. **Run the application:**
   ```bash
   go run main.go
   ```

## ⌨️ Keybindings (Default)

Powerkan is designed to keep your hands on the home row:

- `h` / `j` / `k` / `l`: Navigate Left / Down / Up / Right
- `Enter`: Open / Select focused item
- `e`: Edit focused item (ticket, field)
- `a`: Add / Create a new ticket or epic
- `d`: Delete (requires confirmation)
- `J` / `K`: Reorder items within a column or list
- `q` / `Ctrl+C`: Quit application

## 🛠 Architecture & CLI Usage

Powerkan acts as both the TUI launcher and a CLI utility for scripting.

**Launch TUI:**

```bash
powerkan
```

**Export a ticket:**

```bash
powerkan export ticket --id <TICKET_ID> --format md
```

**Trigger Sprint-End Webhook (Designed for cron):**

```bash
powerkan webhook sprint-end
```

## 🤝 Contributing

This project is currently a solo learning endeavor focused on mastering Go and the Bubble Tea framework. While pull requests are not actively being sought at this very early stage, feedback, architecture discussions, and issue reports are highly welcome!
