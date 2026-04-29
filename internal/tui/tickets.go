package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amedespinosa/powerkan/internal/kanban"
)

var ticketTableColumns = []string{
	"Ticket ID",
	"Title",
	"Epic",
	"Type",
	"Story Points",
	"Sprint",
	"Flag",
	"Github PR",
}

var detailFields = []string{
	"Title",
	"Description",
	"Story Points",
	"Status",
	"Epic",
	"Type",
	"Sprint",
	"Blocked",
	"Github PR URL",
	"New Comment",
}

func (m Model) handleTicketsNormalKey(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "j", "down":
		if m.tickets.focusedRow < len(m.tickets.data.Tickets)-1 {
			m.tickets.focusedRow++
		}
	case "k", "up":
		if m.tickets.focusedRow > 0 {
			m.tickets.focusedRow--
		}
	case "h", "left":
		if m.tickets.focusedColumn > 0 {
			m.tickets.focusedColumn--
		}
	case "l", "right":
		if m.tickets.focusedColumn < len(ticketTableColumns)-1 {
			m.tickets.focusedColumn++
		}
	case "i", "e":
		if selected := m.selectedTableTicket(); selected != nil && m.tickets.focusedColumn > 0 {
			m.mode = modeInsert
			m.tickets.editingCell = true
			m.tickets.originalValue = ticketCellValue(*selected, m.tickets.focusedColumn)
			m.tickets.editingValue = m.tickets.originalValue
			m.statusMessage = "Editing table cell"
		}
	case "enter":
		if selected := m.selectedTableTicket(); selected != nil {
			return m.openDetail(selected.TicketID)
		}
	}
	if selected := m.selectedTableTicket(); selected != nil {
		m.loadSelectedTicket(selected.TicketID)
	}
	return m
}

func (m Model) ticketsView(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("250"))
	colWidth := max((width-6)/len(ticketTableColumns), 12)
	var b strings.Builder
	for idx, column := range ticketTableColumns {
		style := lipgloss.NewStyle().Width(colWidth).Bold(true)
		if idx == m.tickets.focusedColumn {
			style = style.Foreground(lipgloss.Color("208"))
		}
		b.WriteString(style.Render(column))
	}
	b.WriteString("\n")
	for rowIdx, ticket := range m.tickets.data.Tickets {
		for colIdx := range ticketTableColumns {
			value := ticketCellValue(ticket, colIdx)
			if m.tickets.editingCell && rowIdx == m.tickets.focusedRow && colIdx == m.tickets.focusedColumn {
				value = m.tickets.editingValue
			}
			style := lipgloss.NewStyle().Width(colWidth)
			if rowIdx == m.tickets.focusedRow && colIdx == m.tickets.focusedColumn {
				style = style.Foreground(lipgloss.Color("208")).Underline(true)
			} else if rowIdx == m.tickets.focusedRow {
				style = style.Foreground(lipgloss.Color("255"))
			}
			b.WriteString(style.Render(truncate(value, colWidth-1)))
		}
		if rowIdx < len(m.tickets.data.Tickets)-1 {
			b.WriteString("\n")
		}
	}
	return box.Render(b.String())
}

func (m Model) commitTableEdit() Model {
	selected := m.selectedTableTicket()
	if selected == nil {
		m.tickets.editingCell = false
		return m
	}
	input := ticketToUpdateInput(*selected)
	value := strings.TrimSpace(m.tickets.editingValue)
	switch m.tickets.focusedColumn {
	case 1:
		input.Title = value
	case 2:
		epicID, err := m.lookupEpicID(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.EpicID = epicID
	case 3:
		tt, err := parseTicketType(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.Type = tt
	case 4:
		points, err := parseStoryPoints(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.StoryPoints = points
	case 5:
		sprintID, err := m.lookupSprintID(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.SprintID = sprintID
	case 6:
		blocked, err := parseBool(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.Blocked = blocked
	case 7:
		input.GitHubPRURL = value
	default:
		m.tickets.editingCell = false
		return m
	}

	detail, err := m.service.UpdateTicket(context.Background(), selected.TicketID, input)
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	m.errorMessage = ""
	m.statusMessage = "Saved " + detail.TicketID
	m.tickets.editingCell = false
	m.refreshBoard(detail.TicketID)
	m.refreshTickets()
	m.loadSelectedTicket(detail.TicketID)
	return m
}

func (m Model) handleDetailNormalKey(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "j", "down":
		if m.detail.focusedField < len(detailFields)-1 {
			m.detail.focusedField++
		}
	case "k", "up":
		if m.detail.focusedField > 0 {
			m.detail.focusedField--
		}
	case "i", "e":
		if m.selectedTicket != nil {
			m.mode = modeInsert
			m.detail.editingField = true
			m.detail.originalValue = detailFieldValue(m.detail.ticket, m.detail.focusedField)
			m.detail.editingValue = m.detail.originalValue
			m.statusMessage = "Editing detail field"
		}
	case "esc":
		m.activeRoute = m.previousRoute
		m.mode = modeNormal
		m.detail.editingField = false
	}
	return m
}

func (m Model) detailView(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("250"))
	if m.selectedTicket == nil {
		return box.Render("No ticket selected")
	}
	m.detail.ticket = *m.selectedTicket
	lines := []string{lipgloss.NewStyle().Bold(true).Render(m.detail.ticket.TicketID)}
	for idx, name := range detailFields {
		value := detailFieldValue(m.detail.ticket, idx)
		if m.detail.editingField && idx == m.detail.focusedField {
			value = m.detail.editingValue
		}
		style := lipgloss.NewStyle()
		if idx == m.detail.focusedField {
			style = style.Foreground(lipgloss.Color("208"))
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s: %s", name, value)))
	}
	lines = append(lines, "", "Comments:")
	if len(m.detail.ticket.Comments) == 0 {
		lines = append(lines, "  None")
	} else {
		for _, comment := range m.detail.ticket.Comments {
			lines = append(lines, "  - "+comment.Body)
		}
	}
	return box.Render(strings.Join(lines, "\n"))
}

func (m Model) commitDetailEdit() Model {
	if m.selectedTicket == nil {
		m.detail.editingField = false
		return m
	}
	if m.detail.focusedField == len(detailFields)-1 {
		body := strings.TrimSpace(m.detail.editingValue)
		if body == "" {
			m.detail.editingField = false
			return m
		}
		_, err := m.service.AddComment(context.Background(), m.selectedTicket.TicketID, kanban.AddCommentInput{
			Kind: inferCommentKind(body),
			Body: body,
		})
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		m.detail.editingField = false
		m.statusMessage = "Comment added"
		m.loadSelectedTicket(m.selectedTicket.TicketID)
		m.detail.ticket = *m.selectedTicket
		m.refreshBoard(m.selectedTicket.TicketID)
		return m
	}

	input := ticketToUpdateInput(m.detail.ticket.Ticket)
	value := strings.TrimSpace(m.detail.editingValue)
	switch m.detail.focusedField {
	case 0:
		input.Title = value
	case 1:
		input.Description = value
	case 2:
		points, err := parseStoryPoints(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.StoryPoints = points
	case 3:
		status, err := parseTicketStatus(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.Status = status
	case 4:
		epicID, err := m.lookupEpicID(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.EpicID = epicID
	case 5:
		tt, err := parseTicketType(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.Type = tt
	case 6:
		sprintID, err := m.lookupSprintID(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.SprintID = sprintID
	case 7:
		blocked, err := parseBool(value)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		input.Blocked = blocked
	case 8:
		input.GitHubPRURL = value
	}
	detail, err := m.service.UpdateTicket(context.Background(), m.selectedTicket.TicketID, input)
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	m.errorMessage = ""
	m.detail.editingField = false
	m.statusMessage = "Saved " + detail.TicketID
	m.selectedTicket = &detail
	m.detail.ticket = detail
	m.refreshBoard(detail.TicketID)
	m.refreshTickets()
	return m
}

func (m Model) handleExportNormalKey(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "h", "left", "l", "right":
		if m.export.format == "md" {
			m.export.format = "csv"
		} else {
			m.export.format = "md"
		}
	case "enter":
		if m.selectedTicket == nil {
			m.statusMessage = "Select a ticket before exporting"
			return m
		}
		var (
			out string
			err error
		)
		if m.export.format == "md" {
			out, err = m.service.ExportTicketMarkdown(context.Background(), m.selectedTicket.TicketID, "")
		} else {
			out, err = m.service.ExportTicketCSV(context.Background(), m.selectedTicket.TicketID, "")
		}
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		m.errorMessage = ""
		m.statusMessage = "Exported to " + out
	}
	return m
}

func (m Model) exportView(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("250"))
	if m.selectedTicket == nil {
		return box.Render("Select a ticket on the board or in the tickets list, then export it here.")
	}
	formatLine := "Format: Markdown"
	if m.export.format == "csv" {
		formatLine = "Format: CSV"
	}
	return box.Render(strings.Join([]string{
		"Current Ticket",
		m.selectedTicket.TicketID,
		m.selectedTicket.Title,
		"",
		formatLine,
		"",
		"Press h/l to change format and Enter to export.",
	}, "\n"))
}

func ticketCellValue(ticket kanban.Ticket, column int) string {
	switch column {
	case 0:
		return ticket.TicketID
	case 1:
		return ticket.Title
	case 2:
		return ticket.EpicName
	case 3:
		return string(ticket.Type)
	case 4:
		return fmt.Sprintf("%d", ticket.StoryPoints)
	case 5:
		if ticket.SprintID == nil {
			return "Backlog"
		}
		return ticket.SprintName
	case 6:
		if ticket.Blocked {
			return "true"
		}
		return "false"
	case 7:
		return ticket.GitHubPRURL
	default:
		return ""
	}
}

func detailFieldValue(ticket kanban.TicketDetail, field int) string {
	switch field {
	case 0:
		return ticket.Title
	case 1:
		return ticket.Description
	case 2:
		return fmt.Sprintf("%d", ticket.StoryPoints)
	case 3:
		return string(ticket.Status)
	case 4:
		return ticket.EpicName
	case 5:
		return string(ticket.Type)
	case 6:
		if ticket.SprintID == nil {
			return "Backlog"
		}
		return ticket.SprintName
	case 7:
		if ticket.Blocked {
			return "true"
		}
		return "false"
	case 8:
		return ticket.GitHubPRURL
	case 9:
		return ""
	default:
		return ""
	}
}

func ticketToUpdateInput(ticket kanban.Ticket) kanban.UpdateTicketInput {
	return kanban.UpdateTicketInput{
		Title:       ticket.Title,
		Status:      ticket.Status,
		Type:        ticket.Type,
		Blocked:     ticket.Blocked,
		StoryPoints: ticket.StoryPoints,
		EpicID:      ticket.EpicID,
		SprintID:    ticket.SprintID,
		GitHubPRURL: ticket.GitHubPRURL,
		Description: ticket.Description,
	}
}

func (m Model) lookupEpicID(name string) (int64, error) {
	for _, epic := range m.tickets.data.Epics {
		if strings.EqualFold(epic.Name, strings.TrimSpace(name)) {
			return epic.ID, nil
		}
	}
	return 0, fmt.Errorf("unknown epic %q", name)
}

func (m Model) lookupSprintID(name string) (*int64, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || strings.EqualFold(trimmed, "backlog") {
		return nil, nil
	}
	for _, sprint := range m.tickets.data.Sprints {
		if strings.EqualFold(sprint.Name, trimmed) {
			id := sprint.ID
			return &id, nil
		}
	}
	return nil, fmt.Errorf("unknown sprint %q", name)
}

func parseTicketStatus(value string) (kanban.TicketStatus, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), " ", "_"))
	for _, status := range kanban.TicketStatuses {
		if string(status) == normalized {
			return status, nil
		}
	}
	return "", fmt.Errorf("unknown status %q", value)
}

func parseTicketType(value string) (kanban.TicketType, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	for _, ticketType := range kanban.TicketTypes {
		if string(ticketType) == normalized {
			return ticketType, nil
		}
	}
	return "", fmt.Errorf("unknown ticket type %q", value)
}

func inferCommentKind(body string) kanban.CommentKind {
	switch {
	case strings.HasPrefix(body, "http://") || strings.HasPrefix(body, "https://"):
		return kanban.CommentKindURL
	case strings.HasPrefix(body, "/") || strings.HasPrefix(body, "./") || strings.HasPrefix(body, "../"):
		return kanban.CommentKindFilePath
	default:
		return kanban.CommentKindText
	}
}

func truncate(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width == 1 {
		return string(runes[:1])
	}
	return string(runes[:width-1]) + "…"
}
