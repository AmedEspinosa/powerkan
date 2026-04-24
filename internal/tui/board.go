package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var boardStatuses = []string{
	"NOT_STARTED",
	"IN_PROGRESS",
	"UNDER_REVIEW",
	"DONE",
}

type boardModel struct {
	focusedColumn int
}

func newBoardModel() boardModel {
	return boardModel{}
}

func (m boardModel) Update(msg tea.KeyMsg) (boardModel, tea.Cmd) {
	switch msg.String() {
	case "h", "left":
		if m.focusedColumn > 0 {
			m.focusedColumn--
		}
	case "l", "right":
		if m.focusedColumn < len(boardStatuses)-1 {
			m.focusedColumn++
		}
	}

	return m, nil
}

func (m boardModel) View(width, height int) string {
	containerStyle := lipgloss.NewStyle().Width(width).Height(height).Padding(1, 1)
	header := renderBoardHeader(width - 2)
	columns := renderBoardColumns(width-2, max(height-6, 8), m.focusedColumn)
	return containerStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, columns))
}

func renderBoardHeader(width int) string {
	box := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("240"))

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229")).Render("Active Sprint Board")
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render(
		"Sprint: Unassigned  |  Days Left: --  |  % Complete: --  |  Points/Day: --",
	)

	return box.Render(lipgloss.JoinVertical(lipgloss.Left, title, meta))
}

func renderBoardColumns(width, height, focused int) string {
	gap := 1
	columnWidth := max((width-(gap*(len(boardStatuses)-1)))/len(boardStatuses), 18)
	columns := make([]string, 0, len(boardStatuses))

	for idx, status := range boardStatuses {
		titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229"))
		bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("248"))
		columnStyle := lipgloss.NewStyle().
			Width(columnWidth).
			Height(height).
			Padding(1, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))

		if idx == focused {
			columnStyle = columnStyle.BorderForeground(lipgloss.Color("62"))
			titleStyle = titleStyle.Foreground(lipgloss.Color("86"))
		}

		content := lipgloss.JoinVertical(
			lipgloss.Left,
			titleStyle.Render(status),
			bodyStyle.Render("No tickets yet"),
			bodyStyle.Render("Press Enter to open the ticket detail placeholder."),
			bodyStyle.Render(fmt.Sprintf("Focused column: %d/%d", idx+1, len(boardStatuses))),
		)
		columns = append(columns, columnStyle.Render(content))
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, columns...)
}
