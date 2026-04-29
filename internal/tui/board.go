package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amedespinosa/powerkan/internal/kanban"
)

var boardStatuses = []kanban.TicketStatus{
	kanban.TicketStatusNotStarted,
	kanban.TicketStatusInProgress,
	kanban.TicketStatusUnderReview,
	kanban.TicketStatusDone,
}

func (m Model) handleBoardNormalKey(msg tea.KeyMsg) Model {
	switch {
	case msg.String() == "h" || msg.String() == "left" || msg.Type == tea.KeyLeft:
		if m.board.focusedColumn > 0 {
			m.board.focusedColumn--
			m.restoreBoardSelection("")
		}
	case msg.String() == "l" || msg.String() == "right" || msg.Type == tea.KeyRight:
		if m.board.focusedColumn < len(m.board.filtered)-1 {
			m.board.focusedColumn++
			m.restoreBoardSelection("")
		}
	case msg.String() == "j" || msg.String() == "down" || msg.Type == tea.KeyDown:
		column := m.currentBoardColumn()
		if len(column.Tickets) > 0 && m.board.focusedRows[m.board.focusedColumn] < len(column.Tickets)-1 {
			m.board.focusedRows[m.board.focusedColumn]++
			m.loadSelectedTicket(column.Tickets[m.board.focusedRows[m.board.focusedColumn]].TicketID)
		}
	case msg.String() == "k" || msg.String() == "up" || msg.Type == tea.KeyUp:
		column := m.currentBoardColumn()
		if len(column.Tickets) > 0 && m.board.focusedRows[m.board.focusedColumn] > 0 {
			m.board.focusedRows[m.board.focusedColumn]--
			m.loadSelectedTicket(column.Tickets[m.board.focusedRows[m.board.focusedColumn]].TicketID)
		}
	case msg.String() == "H":
		if selected := m.selectedBoardTicket(); selected != nil {
			detail, err := m.service.MoveTicket(contextBackground(), selected.TicketID, -1)
			if err != nil {
				m.errorMessage = err.Error()
				return m
			}
			m.statusMessage = "Moved " + detail.TicketID + " to " + boardDisplayTitle(detail.Status)
			m.refreshBoard(detail.TicketID)
			m.refreshTickets()
		}
	case msg.String() == "L":
		if selected := m.selectedBoardTicket(); selected != nil {
			detail, err := m.service.MoveTicket(contextBackground(), selected.TicketID, 1)
			if err != nil {
				m.errorMessage = err.Error()
				return m
			}
			m.statusMessage = "Moved " + detail.TicketID + " to " + boardDisplayTitle(detail.Status)
			m.refreshBoard(detail.TicketID)
			m.refreshTickets()
		}
	case msg.String() == "s":
		m.mode = modeInsert
		m.board.originalQuery = m.board.searchQuery
		m.statusMessage = "Editing board search"
	case msg.String() == "f":
		m.board.filter.blockedOnly = !m.board.filter.blockedOnly
		m.applyBoardFilters()
		m.restoreBoardSelection(m.currentSelectedTicketID())
		if m.board.filter.blockedOnly {
			m.statusMessage = "Board filter: blocked only"
		} else {
			m.statusMessage = "Board filter cleared"
		}
	case msg.String() == "enter" || msg.Type == tea.KeyEnter:
		if selected := m.selectedBoardTicket(); selected != nil {
			return m.openDetail(selected.TicketID)
		}
	}
	return m
}

func (m Model) boardView(width, height int) string {
	leftWidth := max(width/3, 38)
	rightWidth := max(width-leftWidth-1, 40)

	sprintPanel := renderSprintPanel(leftWidth, max(10, height/4), m.board.data.Metrics)
	detailPanel := renderSelectedTicketPanel(leftWidth, height-lipgloss.Height(sprintPanel), m.selectedTicket)
	left := lipgloss.JoinVertical(lipgloss.Left, sprintPanel, detailPanel)
	right := renderBoardColumns(rightWidth, height, m.board.filtered, m.board.focusedColumn, m.board.focusedRows)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

func renderSprintPanel(width, height int, metrics kanban.BoardMetrics) string {
	title := "No Active Sprint"
	rangeText := "No sprint scheduled"
	if metrics.Sprint != nil {
		title = metrics.Sprint.Name
		rangeText = fmt.Sprintf("%s - %s", metrics.Sprint.StartDate.Format("Jan 2"), metrics.Sprint.EndDate.Format("Jan 2"))
	}
	calendar := []string{
		"[] [] [] [] [] [] []",
		"[] [] [] [] [] [] []",
		"[] [] [] [] [] [] []",
		"Phase 2 calendar placeholder",
	}
	stats := []string{
		fmt.Sprintf("Sprint Days Left: %d", metrics.DaysLeft),
		fmt.Sprintf("%% of Points Completed: %.2f%%", metrics.PercentCompleted),
		fmt.Sprintf("Points Per Day: %.2f", metrics.PointsPerDay),
	}
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("250"))
	titleStyle := lipgloss.NewStyle().Bold(true)
	return box.Render(lipgloss.JoinVertical(lipgloss.Left,
		titleStyle.Render(title),
		rangeText,
		"",
		strings.Join(calendar, "\n"),
		"",
		strings.Join(stats, "\n"),
	))
}

func renderSelectedTicketPanel(width, height int, ticket *kanban.TicketDetail) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("250"))
	if ticket == nil {
		return box.Render("No ticket selected")
	}

	commentLines := []string{"Comments:"}
	if len(ticket.Comments) == 0 {
		commentLines = append(commentLines, "  None")
	} else {
		for _, comment := range ticket.Comments {
			commentLines = append(commentLines, "  - "+comment.Body)
		}
	}

	sprint := "Backlog"
	if ticket.SprintID != nil {
		sprint = ticket.SprintName
	}
	pr := ticket.GitHubPRURL
	if pr == "" {
		pr = "-"
	}
	return box.Render(lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Bold(true).Render(ticket.TicketID),
		ticket.Title,
		"",
		"Description:",
		ticket.Description,
		"",
		fmt.Sprintf("Story Points: %d", ticket.StoryPoints),
		fmt.Sprintf("Flag: %t", ticket.Blocked),
		fmt.Sprintf("Parent: %s", ticket.EpicName),
		fmt.Sprintf("Type: %s", ticket.Type),
		fmt.Sprintf("GitHub PR: %s", pr),
		fmt.Sprintf("Sprint: %s", sprint),
		"",
		strings.Join(commentLines, "\n"),
	))
}

func renderBoardColumns(width, height int, columns []kanban.BoardColumn, focused int, focusedRows []int) string {
	gap := 1
	columnWidth := max((width-(gap*(len(boardStatuses)-1)))/len(boardStatuses), 20)
	rendered := make([]string, 0, len(boardStatuses))
	for idx, column := range columns {
		titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
		borderColor := lipgloss.Color("250")
		if idx == focused {
			borderColor = lipgloss.Color("208")
			titleStyle = titleStyle.Foreground(lipgloss.Color("208"))
		}

		cards := make([]string, 0, max(1, len(column.Tickets)))
		if len(column.Tickets) == 0 {
			empty := lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("No tickets")
			if idx == focused {
				empty = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render("No tickets")
			}
			cards = append(cards, empty)
		}
		for rowIdx, ticket := range column.Tickets {
			cardBorder := lipgloss.Color("245")
			if idx == focused && rowIdx == focusedRows[idx] {
				cardBorder = lipgloss.Color("208")
			}
			card := lipgloss.NewStyle().
				Width(columnWidth-4).
				Padding(0, 1).
				Border(lipgloss.RoundedBorder()).
				BorderForeground(cardBorder).
				Render(lipgloss.JoinVertical(lipgloss.Left,
					ticket.TicketID,
					ticket.Title,
					ticket.EpicName,
					fmt.Sprintf("%d pts", ticket.StoryPoints),
				))
			cards = append(cards, card)
		}

		columnBox := lipgloss.NewStyle().
			Width(columnWidth).
			Height(height).
			Padding(0, 0).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(borderColor).
			Render(lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(boardDisplayTitle(column.Status)), strings.Join(cards, "\n")))
		rendered = append(rendered, columnBox)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, rendered...)
}

func (m Model) currentBoardColumn() kanban.BoardColumn {
	if len(m.board.filtered) == 0 || m.board.focusedColumn >= len(m.board.filtered) {
		return kanban.BoardColumn{}
	}
	return m.board.filtered[m.board.focusedColumn]
}

func boardDisplayTitle(status kanban.TicketStatus) string {
	switch status {
	case kanban.TicketStatusNotStarted:
		return "Not Started"
	case kanban.TicketStatusInProgress:
		return "In Progress"
	case kanban.TicketStatusUnderReview:
		return "Under Review"
	case kanban.TicketStatusDone:
		return "Completed"
	default:
		return string(status)
	}
}

func contextBackground() context.Context {
	return context.Background()
}
