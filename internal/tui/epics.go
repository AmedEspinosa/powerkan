package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amedespinosa/powerkan/internal/kanban"
)

const (
	epicFormFieldTitle = iota
	epicFormFieldStatus
	epicFormFieldColor
	epicFormFieldSubmit
	epicFormFieldCancel
)

func (m Model) handleEpicsNormalKey(msg tea.KeyMsg) Model {
	if m.epics.modal == epicModalForm {
		return m.handleEpicFormKey(msg)
	}
	if m.epics.modal == epicModalPicker {
		return m.handleEpicPickerKey(msg)
	}
	switch msg.String() {
	case "j", "down":
		if m.epics.focusedRow < len(m.epics.epics)-1 {
			m.epics.focusedRow++
		}
	case "k", "up":
		if m.epics.focusedRow > 0 {
			m.epics.focusedRow--
		}
	case "n":
		return m.openEpicForm(nil)
	case "e", "enter":
		return m.openEpicForm(m.focusedEpic())
	}
	return m
}

func (m Model) epicsView(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("250"))
	lines := []string{lipgloss.NewStyle().Bold(true).Render("Epics"), ""}
	if len(m.epics.epics) == 0 {
		lines = append(lines, "No epics")
	} else {
		for idx, epic := range m.epics.epics {
			prefix := "  "
			style := lipgloss.NewStyle()
			if idx == m.epics.focusedRow {
				prefix = "> "
				style = style.Foreground(lipgloss.Color("208"))
			}
			color := ""
			if epic.Color != nil {
				color = " " + *epic.Color
			}
			lines = append(lines, style.Render(fmt.Sprintf("%s%s [%s]%s", prefix, epic.Title, epic.Status, color)))
		}
	}
	if m.epics.modal == epicModalForm {
		lines = append(lines, "", m.renderEpicForm(max(44, width-6), max(12, height/2)))
	}
	if m.epics.modal == epicModalPicker {
		lines = append(lines, "", m.renderPicker(m.epics.picker, max(48, width-6), max(10, height/3)))
	}
	return box.Render(strings.Join(lines, "\n"))
}

func (m Model) focusedEpic() *kanban.Epic {
	if m.epics.focusedRow < 0 || m.epics.focusedRow >= len(m.epics.epics) {
		return nil
	}
	epic := m.epics.epics[m.epics.focusedRow]
	return &epic
}

func (m Model) openEpicForm(epic *kanban.Epic) Model {
	form := epicFormModel{status: kanban.EpicStatusOpen, errors: map[int]string{}, color: ""}
	if epic != nil {
		form.editID = epic.ID
		form.title = epic.Title
		form.status = epic.Status
		if epic.Color != nil {
			form.color = *epic.Color
		}
	}
	m.epics.form = form
	m.epics.modal = epicModalForm
	m.mode = modeNormal
	return m
}

func (m Model) handleEpicFormKey(msg tea.KeyMsg) Model {
	if m.isEpicFormTextField() && msg.Type == tea.KeyRunes && isPrintableRunes(msg.Runes) {
		return m.beginEpicFormEdit(string(msg.Runes))
	}
	switch msg.String() {
	case "j", "down", "tab":
		m.epics.form.focusedField = min(m.epics.form.focusedField+1, epicFormFieldCancel)
	case "k", "up", "shift+tab":
		m.epics.form.focusedField = max(0, m.epics.form.focusedField-1)
	case "h", "left", "l", "right":
		if m.epics.form.focusedField == epicFormFieldStatus {
			if m.epics.form.status == kanban.EpicStatusOpen {
				m.epics.form.status = kanban.EpicStatusDone
			} else {
				m.epics.form.status = kanban.EpicStatusOpen
			}
		}
	case "esc":
		m.epics.modal = epicModalNone
	case "i", "e":
		if m.isEpicFormTextField() {
			return m.beginEpicFormEdit("")
		}
	case "enter":
		switch m.epics.form.focusedField {
		case epicFormFieldTitle, epicFormFieldColor:
			return m.beginEpicFormEdit("")
		case epicFormFieldSubmit:
			return m.submitEpicForm()
		case epicFormFieldCancel:
			m.epics.modal = epicModalNone
		}
	}
	return m
}

func (m Model) renderEpicForm(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("208"))
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render("Epic Form"),
		"j/k move, h/l change status, Enter edit/save, Esc cancel",
		"",
	}
	fields := []struct {
		label string
		value string
		err   string
	}{
		{"Title", m.epicFormFieldValue(epicFormFieldTitle), m.epics.form.errors[epicFormFieldTitle]},
		{"Status", string(m.epics.form.status), ""},
		{"Color", m.epicFormFieldValue(epicFormFieldColor), ""},
		{"Save", "[Save]", ""},
		{"Cancel", "[Cancel]", ""},
	}
	for idx, field := range fields {
		style := lipgloss.NewStyle()
		prefix := "  "
		if idx == m.epics.form.focusedField {
			prefix = "> "
			style = style.Foreground(lipgloss.Color("208"))
		}
		lines = append(lines, style.Render(prefix+field.label+": "+field.value))
		if field.err != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("    "+field.err))
		}
	}
	return box.Render(strings.Join(lines, "\n"))
}

func (m Model) epicFormFieldValue(field int) string {
	if m.epics.form.editingField && field == m.epics.form.focusedField {
		return m.epics.form.editingValue
	}
	switch field {
	case epicFormFieldTitle:
		return m.epics.form.title
	case epicFormFieldColor:
		if strings.TrimSpace(m.epics.form.color) == "" {
			return "(none)"
		}
		return m.epics.form.color
	default:
		return ""
	}
}

func (m Model) isEpicFormTextField() bool {
	return m.epics.form.focusedField == epicFormFieldTitle || m.epics.form.focusedField == epicFormFieldColor
}

func (m Model) beginEpicFormEdit(seed string) Model {
	m.mode = modeInsert
	m.epics.form.editingField = true
	if seed == "" {
		m.epics.form.editingValue = m.epicFormFieldValue(m.epics.form.focusedField)
	} else {
		m.epics.form.editingValue = seed
	}
	return m
}

func (m Model) commitEpicFormInsert() Model {
	if !m.epics.form.editingField {
		return m
	}
	switch m.epics.form.focusedField {
	case epicFormFieldTitle:
		m.epics.form.title = m.epics.form.editingValue
	case epicFormFieldColor:
		m.epics.form.color = strings.TrimSpace(strings.ReplaceAll(m.epics.form.editingValue, "(none)", ""))
	}
	m.epics.form.editingField = false
	m.epics.form.editingValue = ""
	return m
}

func (m Model) submitEpicForm() Model {
	m.epics.form.errors = map[int]string{}
	if strings.TrimSpace(m.epics.form.title) == "" {
		m.epics.form.errors[epicFormFieldTitle] = "Title is required"
		m.errorMessage = "Fix validation errors"
		return m
	}
	var color *string
	if trimmed := strings.TrimSpace(m.epics.form.color); trimmed != "" {
		color = &trimmed
	}
	if m.epics.form.editID == 0 {
		created, err := m.service.CreateEpic(context.Background(), kanban.CreateEpicInput{Title: m.epics.form.title, Status: m.epics.form.status, Color: color})
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		m.statusMessage = "Created " + created.Title
	} else {
		updated, err := m.service.UpdateEpic(context.Background(), m.epics.form.editID, kanban.UpdateEpicInput{Title: m.epics.form.title, Status: m.epics.form.status, Color: color})
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		m.statusMessage = "Saved " + updated.Title
	}
	m.errorMessage = ""
	m.refreshAllForTicketChange(m.currentSelectedTicketID())
	m.epics.modal = epicModalNone
	return m
}

func (m *Model) openEpicAssignmentPicker(ticketID string, allowClear bool) {
	items := []pickerItem{}
	if allowClear {
		items = append(items, pickerItem{key: "clear", label: "Clear epic", description: "remove assignment"})
	}
	for _, epic := range m.epics.epics {
		items = append(items, pickerItem{
			key:         fmt.Sprintf("%d", epic.ID),
			label:       epic.Title,
			description: string(epic.Status),
			valueID:     epic.ID,
		})
	}
	m.epics.picker = pickerModel{
		kind:         pickerKindEpicAssignment,
		title:        "Assign Epic",
		emptyMessage: "No epics available.",
		ticketID:     ticketID,
		allowClear:   allowClear,
		items:        items,
	}
	m.epics.modal = epicModalPicker
}

func (m Model) handleEpicPickerKey(msg tea.KeyMsg) Model {
	picker := &m.epics.picker
	items := m.filteredPickerItems(*picker)
	m.normalizePickerFocus(picker)
	items = m.filteredPickerItems(*picker)
	switch msg.String() {
	case "esc":
		m.epics.modal = epicModalNone
	case "j", "down":
		if picker.focused < len(items)-1 {
			picker.focused++
		}
	case "k", "up":
		if picker.focused > 0 {
			picker.focused--
		}
	case "backspace":
		if picker.query != "" {
			picker.query = trimLastRune(picker.query)
			m.normalizePickerFocus(picker)
		}
	case "enter":
		if len(items) == 0 {
			return m
		}
		selected := items[picker.focused]
		m.epics.modal = epicModalNone
		if selected.key == "clear" {
			return m.clearEpicForTicket(picker.ticketID)
		}
		return m.assignEpicToTicket(picker.ticketID, selected.valueID)
	default:
		if msg.Type == tea.KeyRunes {
			picker.query += string(msg.Runes)
			m.normalizePickerFocus(picker)
		}
	}
	return m
}

func (m Model) assignEpicToTicket(ticketID string, epicID int64) Model {
	detail, err := m.service.AssignEpicToTicket(context.Background(), ticketID, epicID)
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	m.statusMessage = "Updated epic for " + detail.TicketID
	m.errorMessage = ""
	m.selectedTicket = &detail
	m.refreshAllForTicketChange(detail.TicketID)
	return m
}

func (m Model) clearEpicForTicket(ticketID string) Model {
	detail, err := m.service.ClearEpicForTicket(context.Background(), ticketID)
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	m.statusMessage = "Cleared epic for " + detail.TicketID
	m.errorMessage = ""
	m.selectedTicket = &detail
	m.refreshAllForTicketChange(detail.TicketID)
	return m
}

func (m Model) clearEpicForSelectedTicket() Model {
	if m.selectedTicket == nil {
		m.errorMessage = "Select a ticket first"
		return m
	}
	return m.clearEpicForTicket(m.selectedTicket.TicketID)
}
