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
	createFieldTitle = iota
	createFieldType
	createFieldStoryPoints
	createFieldDescription
	createFieldEpic
	createFieldSprint
	createFieldSubmit
	createFieldCancel
)

var createFieldLabels = []string{
	"Title",
	"Type",
	"Story Points",
	"Description",
	"Epic",
	"Sprint",
	"Create",
	"Cancel",
}

func (m Model) handleCreateTicketNormalKey(msg tea.KeyMsg) Model {
	if m.create.picker.kind != createPickerNone {
		return m.handleCreatePickerKey(msg)
	}

	if m.isCreateTextField() && isSpaceKey(msg) {
		return m.beginCreateFieldEdit(" ")
	}

	if m.isCreateTextField() && msg.Type == tea.KeyRunes && isPrintableRunes(msg.Runes) {
		return m.beginCreateFieldEdit(string(msg.Runes))
	}

	switch {
	case msg.String() == "tab" || msg.String() == "j" || msg.String() == "down" || msg.Type == tea.KeyDown:
		m.create.focusedField = min(m.create.focusedField+1, len(createFieldLabels)-1)
	case msg.String() == "shift+tab" || msg.String() == "k" || msg.String() == "up" || msg.Type == tea.KeyUp:
		m.create.focusedField = max(0, m.create.focusedField-1)
	case msg.String() == "i" || msg.String() == "e":
		if m.isCreateTextField() {
			return m.beginCreateFieldEdit("")
		}
	case msg.String() == "h" || msg.String() == "left" || msg.Type == tea.KeyLeft:
		if m.create.focusedField == createFieldType {
			m.create.ticketType = cycleTicketType(m.create.ticketType, -1)
		}
	case msg.String() == "l" || msg.String() == "right" || msg.Type == tea.KeyRight:
		if m.create.focusedField == createFieldType {
			m.create.ticketType = cycleTicketType(m.create.ticketType, 1)
		}
	case msg.String() == "enter" || msg.Type == tea.KeyEnter:
		switch m.create.focusedField {
		case createFieldTitle, createFieldStoryPoints, createFieldDescription:
			return m.beginCreateFieldEdit("")
		case createFieldEpic:
			m.openCreatePicker(createPickerEpic)
		case createFieldSprint:
			m.openCreatePicker(createPickerSprint)
		case createFieldSubmit:
			return m.submitCreateTicket()
		case createFieldCancel:
			return m.exitCreateTicket()
		}
	case msg.String() == "esc" || msg.Type == tea.KeyEsc:
		return m.exitCreateTicket()
	default:
		if m.isCreateTextField() && msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			return m.beginCreateFieldEdit(string(msg.Runes))
		}
	}
	return m
}

func (m Model) beginCreateFieldEdit(seed string) Model {
	m.mode = modeInsert
	m.create.editingField = true
	if seed == "" {
		m.create.editingValue = m.currentCreateFieldValue()
		return m
	}
	m.create.editingValue = seed
	return m
}

func (m Model) openCreateTicket(caller route, defaultSprintID *int64) Model {
	m.refreshSupportData()
	defaultEpic, err := m.service.EnsureDefaultEpic(context.Background())
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	m.refreshSupportData()

	model := createTicketModel{
		callerRoute:          caller,
		callerSelectedTicket: m.currentSelectedTicketID(),
		ticketType:           kanban.TicketTypeFeature,
		storyPoints:          "0",
		defaultSprintID:      cloneInt64Pointer(defaultSprintID),
		errors:               make(map[int]string),
		epicID:               defaultEpic.ID,
		epicName:             defaultEpic.Name,
	}

	switch {
	case defaultSprintID != nil:
		if sprintName, ok := m.lookupSprintName(defaultSprintID); ok {
			model.sprintID = cloneInt64Pointer(defaultSprintID)
			model.sprintName = sprintName
		}
	case len(m.tickets.data.Sprints) == 0:
		model.sprintHint = "No sprints exist yet. Ticket will be created in Backlog."
	default:
		model.sprintHint = "No active sprint found. Press Enter on Sprint to choose an existing sprint."
	}

	m.create = model
	m.activeRoute = routeCreateTicket
	m.mode = modeNormal
	m.errorMessage = ""
	m.statusMessage = "Create ticket"
	return m
}

func (m Model) exitCreateTicket() Model {
	m.activeRoute = m.create.callerRoute
	m.mode = modeNormal
	m.create = createTicketModel{}
	m.errorMessage = ""
	return m
}

func (m Model) currentCreateFieldValue() string {
	switch m.create.focusedField {
	case createFieldTitle:
		return m.create.title
	case createFieldStoryPoints:
		return m.create.storyPoints
	case createFieldDescription:
		return m.create.description
	default:
		return ""
	}
}

func (m Model) isCreateTextField() bool {
	switch m.create.focusedField {
	case createFieldTitle, createFieldStoryPoints, createFieldDescription:
		return true
	default:
		return false
	}
}

func (m Model) commitCreateInsert() Model {
	if m.create.picker.editingQuery {
		m.create.picker.editingQuery = false
		return m
	}
	if !m.create.editingField {
		return m
	}
	switch m.create.focusedField {
	case createFieldTitle:
		m.create.title = m.create.editingValue
	case createFieldStoryPoints:
		m.create.storyPoints = m.create.editingValue
	case createFieldDescription:
		m.create.description = m.create.editingValue
	}
	m.create.editingField = false
	m.create.editingValue = ""
	return m
}

func (m Model) cancelCreateInsert() Model {
	if m.create.picker.editingQuery {
		m.create.picker.editingQuery = false
		return m
	}
	m.create.editingField = false
	m.create.editingValue = ""
	return m
}

func (m *Model) openCreatePicker(kind createPickerKind) {
	m.create.picker = createPickerModel{kind: kind}
	m.mode = modeNormal
}

func (m Model) handleCreatePickerKey(msg tea.KeyMsg) Model {
	items := m.filteredCreatePickerItems()
	m.normalizeCreatePickerFocus(len(items))
	items = m.filteredCreatePickerItems()
	switch {
	case msg.String() == "tab" || msg.String() == "j" || msg.String() == "down" || msg.Type == tea.KeyDown:
		if m.create.picker.focused < len(items)-1 {
			m.create.picker.focused++
		}
	case msg.String() == "shift+tab" || msg.String() == "k" || msg.String() == "up" || msg.Type == tea.KeyUp:
		if m.create.picker.focused > 0 {
			m.create.picker.focused--
		}
	case msg.String() == "i" || msg.String() == "e":
		m.mode = modeInsert
		m.create.picker.editingQuery = true
	case msg.String() == "enter" || msg.Type == tea.KeyEnter:
		if len(items) == 0 {
			return m
		}
		selected := items[m.create.picker.focused]
		switch m.create.picker.kind {
		case createPickerEpic:
			m.create.epicID = selected.epicID
			m.create.epicName = selected.label
		case createPickerSprint:
			m.create.sprintID = cloneInt64Pointer(selected.sprintID)
			m.create.sprintName = selected.label
			if selected.sprintID == nil {
				m.create.sprintHint = ""
			}
		}
		m.create.picker = createPickerModel{}
	case msg.String() == "esc" || msg.Type == tea.KeyEsc:
		m.create.picker = createPickerModel{}
		m.mode = modeNormal
	default:
		if msg.Type == tea.KeyRunes && !m.create.picker.editingQuery {
			m.mode = modeInsert
			m.create.picker.editingQuery = true
			m.create.picker.query += string(msg.Runes)
			m.normalizeCreatePickerFocus(len(m.filteredCreatePickerItems()))
		}
	}
	return m
}

func (m *Model) normalizeCreatePickerFocus(length int) {
	if length == 0 {
		m.create.picker.focused = 0
		return
	}
	if m.create.picker.focused >= length {
		m.create.picker.focused = length - 1
	}
}

func (m Model) createTicketView(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("250"))
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render("Create Ticket"),
		"Tab/Shift+Tab or j/k move, Enter edit/select/save, type to edit text, h/l change type, Esc cancel",
		"1-4 routes disabled while creating",
		"",
	}

	for idx, label := range createFieldLabels {
		value := m.createFieldDisplayValue(idx)
		prefix := "  "
		style := lipgloss.NewStyle()
		if idx == m.create.focusedField {
			prefix = "> "
			style = style.Foreground(lipgloss.Color("208"))
		}
		lines = append(lines, style.Render(prefix+label+": "+value))
		if idx == createFieldSprint && m.create.sprintHint != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Render("    "+m.create.sprintHint))
		}
		if msg := m.create.errors[idx]; msg != "" {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("    "+msg))
		}
	}

	if m.create.picker.kind != createPickerNone {
		lines = append(lines, "", m.renderCreatePicker(max(40, width-6), max(9, height/3)))
	}

	return box.Render(strings.Join(lines, "\n"))
}

func (m Model) createFieldDisplayValue(field int) string {
	switch field {
	case createFieldTitle:
		if m.create.editingField && field == m.create.focusedField {
			return m.create.editingValue
		}
		return m.create.title
	case createFieldType:
		return string(m.create.ticketType)
	case createFieldStoryPoints:
		if m.create.editingField && field == m.create.focusedField {
			return m.create.editingValue
		}
		return m.create.storyPoints
	case createFieldDescription:
		if m.create.editingField && field == m.create.focusedField {
			return m.create.editingValue
		}
		if strings.TrimSpace(m.create.description) == "" {
			return "(empty)"
		}
		return m.create.description
	case createFieldEpic:
		return fmt.Sprintf("%s (Enter to choose)", m.create.epicName)
	case createFieldSprint:
		name := "Backlog"
		if m.create.sprintID != nil {
			name = m.create.sprintName
		}
		return fmt.Sprintf("%s (Enter to choose)", name)
	case createFieldSubmit:
		return "[Create]"
	case createFieldCancel:
		return "[Cancel]"
	default:
		return ""
	}
}

type createPickerItem struct {
	label    string
	epicID   int64
	sprintID *int64
}

func (m Model) filteredCreatePickerItems() []createPickerItem {
	query := strings.ToLower(strings.TrimSpace(m.create.picker.query))
	items := make([]createPickerItem, 0)
	switch m.create.picker.kind {
	case createPickerEpic:
		for _, epic := range m.tickets.data.Epics {
			display := fmt.Sprintf("%s (#%d)", epic.Name, epic.ID)
			if query != "" && !strings.Contains(strings.ToLower(display), query) {
				continue
			}
			items = append(items, createPickerItem{label: epic.Name, epicID: epic.ID})
		}
	case createPickerSprint:
		backlog := createPickerItem{label: "Backlog", sprintID: nil}
		if query == "" || strings.Contains(strings.ToLower(backlog.label), query) {
			items = append(items, backlog)
		}
		for _, sprint := range m.tickets.data.Sprints {
			display := fmt.Sprintf("%s (#%d)", sprint.Name, sprint.ID)
			if query != "" && !strings.Contains(strings.ToLower(display), query) {
				continue
			}
			id := sprint.ID
			items = append(items, createPickerItem{label: sprint.Name, sprintID: &id})
		}
	}
	return items
}

func (m Model) renderCreatePicker(width, height int) string {
	items := m.filteredCreatePickerItems()
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(m.createPickerTitle()),
		"type to search, j/k move, Enter select, Esc close",
		"Query: " + m.create.picker.query,
		"",
	}
	if len(items) == 0 {
		lines = append(lines, m.createPickerEmptyState())
		return lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("208")).Render(strings.Join(lines, "\n"))
	}

	for idx, item := range items {
		prefix := "  "
		style := lipgloss.NewStyle()
		if idx == m.create.picker.focused {
			prefix = "> "
			style = style.Foreground(lipgloss.Color("208"))
		}
		lines = append(lines, style.Render(prefix+item.label))
	}
	return lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("208")).Render(strings.Join(lines, "\n"))
}

func (m Model) createPickerTitle() string {
	switch m.create.picker.kind {
	case createPickerEpic:
		return "Pick Epic"
	case createPickerSprint:
		return "Pick Sprint"
	default:
		return ""
	}
}

func (m Model) createPickerEmptyState() string {
	if m.create.picker.kind == createPickerSprint {
		return "No sprint records exist yet. Choose Backlog or create a sprint elsewhere."
	}
	return "No matching epics."
}

func (m Model) submitCreateTicket() Model {
	m.create.errors = make(map[int]string)
	title := strings.TrimSpace(m.create.title)
	if title == "" {
		m.create.errors[createFieldTitle] = "Title is required"
	}

	points, err := parseStoryPoints(m.create.storyPoints)
	if err != nil || points < 0 {
		m.create.errors[createFieldStoryPoints] = "Story points must be a number >= 0"
	}
	if m.create.epicID == 0 {
		m.create.errors[createFieldEpic] = "Epic is required"
	}
	if len(m.create.errors) > 0 {
		m.errorMessage = "Fix validation errors"
		return m
	}

	detail, err := m.service.CreateTicket(context.Background(), kanban.CreateTicketInput{
		Title:       title,
		Status:      kanban.TicketStatusNotStarted,
		Type:        m.create.ticketType,
		Blocked:     false,
		StoryPoints: points,
		EpicID:      m.create.epicID,
		SprintID:    cloneInt64Pointer(m.create.sprintID),
		GitHubPRURL: "",
		Description: m.create.description,
	})
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}

	m.statusMessage = "Created " + detail.TicketID
	m.errorMessage = ""
	m.selectedTicket = &detail
	m.refreshSupportData()
	m.refreshTicketsByInternalID(detail.ID)
	switch m.create.callerRoute {
	case routeBoard:
		m.refreshBoard(m.create.callerSelectedTicket)
		if m.canFocusCreatedBoardTicket(detail) {
			m.focusBoardTicketByInternalID(detail.ID)
		}
	case routeTickets:
		m.refreshBoard("")
		m.focusTableTicketByInternalID(detail.ID)
		if selected := m.selectedTableTicket(); selected != nil {
			m.loadSelectedTicket(selected.TicketID)
		}
	}
	caller := m.create.callerRoute
	m.create = createTicketModel{}
	m.activeRoute = caller
	m.mode = modeNormal
	return m
}

func (m Model) canFocusCreatedBoardTicket(detail kanban.TicketDetail) bool {
	if m.board.data.Metrics.Sprint == nil || detail.SprintID == nil {
		return false
	}
	return m.board.data.Metrics.Sprint.ID == *detail.SprintID
}

func cycleTicketType(current kanban.TicketType, delta int) kanban.TicketType {
	index := 0
	for idx, candidate := range kanban.TicketTypes {
		if candidate == current {
			index = idx
			break
		}
	}
	index = (index + delta + len(kanban.TicketTypes)) % len(kanban.TicketTypes)
	return kanban.TicketTypes[index]
}

func cloneInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (m Model) lookupSprintName(sprintID *int64) (string, bool) {
	if sprintID == nil {
		return "Backlog", true
	}
	for _, sprint := range m.tickets.data.Sprints {
		if sprint.ID == *sprintID {
			return sprint.Name, true
		}
	}
	return "", false
}

func isPrintableRunes(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	return true
}

func isSpaceKey(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeySpace || msg.String() == "space"
}
