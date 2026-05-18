package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amedespinosa/powerkan/internal/kanban"
)

const (
	sprintFormFieldName = iota
	sprintFormFieldGoal
	sprintFormFieldStartsOn
	sprintFormFieldEndsOn
	sprintFormFieldSubmit
	sprintFormFieldCancel
)

func (m Model) handleSprintsNormalKey(msg tea.KeyMsg) Model {
	if m.sprints.modal == sprintModalForm {
		return m.handleSprintFormKey(msg)
	}
	if m.sprints.modal == sprintModalCloseWizard {
		return m.handleSprintCloseWizardKey(msg)
	}
	if m.sprints.modal == sprintModalPicker {
		return m.handleSprintPickerKey(msg)
	}

	switch msg.String() {
	case "j", "down":
		if m.sprints.focusedRow < len(m.sprints.summaries)-1 {
			m.sprints.focusedRow++
		}
	case "k", "up":
		if m.sprints.focusedRow > 0 {
			m.sprints.focusedRow--
		}
	case "n":
		return m.openSprintForm(nil)
	case "e":
		return m.openSprintForm(m.focusedSprintSummary())
	case "a":
		return m.activateFocusedSprint()
	case "c":
		return m.openCloseSprintWizard()
	case "enter":
		if sprint := m.focusedSprintSummary(); sprint != nil {
			if sprint.Status == kanban.SprintStatusActive {
				return m.openCloseSprintWizard()
			}
			return m.openSprintForm(sprint)
		}
	}
	return m
}

func (m Model) sprintsView(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("250"))
	lines := []string{lipgloss.NewStyle().Bold(true).Render("Sprints")}
	if len(m.sprints.summaries) == 0 {
		lines = append(lines, "", "No sprints")
	} else {
		lines = append(lines, "")
		lines = append(lines, m.renderSprintGroup("Active", kanban.SprintStatusActive))
		lines = append(lines, "")
		lines = append(lines, m.renderSprintGroup("Planned", kanban.SprintStatusPlanned))
		lines = append(lines, "")
		lines = append(lines, m.renderSprintGroup("Closed", kanban.SprintStatusClosed))
	}
	if m.sprints.modal == sprintModalForm {
		lines = append(lines, "", m.renderSprintForm(max(48, width-6), max(14, height/2)))
	}
	if m.sprints.modal == sprintModalPicker {
		lines = append(lines, "", m.renderPicker(m.sprints.picker, max(48, width-6), max(10, height/3)))
	}
	if m.sprints.modal == sprintModalCloseWizard {
		lines = append(lines, "", m.renderCloseSprintWizard(max(56, width-6), max(18, height-8)))
	}
	return box.Render(strings.Join(lines, "\n"))
}

func (m Model) renderSprintGroup(title string, status kanban.SprintStatus) string {
	lines := []string{lipgloss.NewStyle().Bold(true).Render(title)}
	count := 0
	for idx, sprint := range m.sprints.summaries {
		if sprint.Status != status {
			continue
		}
		prefix := "  "
		style := lipgloss.NewStyle()
		if idx == m.sprints.focusedRow {
			prefix = "> "
			style = style.Foreground(lipgloss.Color("208"))
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%s  %s to %s  %.0f%% complete  %d tickets",
			prefix,
			sprint.Name,
			sprint.StartsOn.Format("2006-01-02"),
			sprint.EndsOn.Format("2006-01-02"),
			sprint.PercentCompleted,
			sprint.TicketCount,
		)))
		if strings.TrimSpace(sprint.Goal) != "" {
			lines = append(lines, "    Goal: "+sprint.Goal)
		}
		count++
	}
	if count == 0 {
		lines = append(lines, "  None")
	}
	return strings.Join(lines, "\n")
}

func (m Model) focusedSprintSummary() *kanban.SprintSummary {
	if m.sprints.focusedRow < 0 || m.sprints.focusedRow >= len(m.sprints.summaries) {
		return nil
	}
	sprint := m.sprints.summaries[m.sprints.focusedRow]
	return &sprint
}

func (m *Model) focusSprintByID(id int64) bool {
	for idx, sprint := range m.sprints.summaries {
		if sprint.ID == id {
			m.sprints.focusedRow = idx
			return true
		}
	}
	return false
}

func (m *Model) focusSprintByStatus(status kanban.SprintStatus) bool {
	for idx, sprint := range m.sprints.summaries {
		if sprint.Status == status {
			m.sprints.focusedRow = idx
			return true
		}
	}
	return false
}

func (m Model) canActivateFocusedSprint() bool {
	sprint := m.focusedSprintSummary()
	return sprint != nil && sprint.Status == kanban.SprintStatusPlanned
}

func (m Model) activateFocusedSprint() Model {
	sprint := m.focusedSprintSummary()
	if sprint == nil {
		m.errorMessage = "No sprint selected"
		return m
	}
	if sprint.Status != kanban.SprintStatusPlanned {
		m.errorMessage = "Only planned sprints can be activated"
		return m
	}
	activated, err := m.service.ActivateSprint(context.Background(), sprint.ID)
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	m.errorMessage = ""
	m.statusMessage = "Activated " + activated.Name
	m.refreshAllForTicketChange(m.currentSelectedTicketID())
	m.focusSprintByID(activated.ID)
	return m
}

func (m *Model) openSprintActivatePicker() {
	items := []pickerItem{}
	for _, sprint := range m.sprints.summaries {
		if sprint.Status == kanban.SprintStatusPlanned {
			items = append(items, pickerItem{key: fmt.Sprintf("%d", sprint.ID), label: sprint.Name, description: "planned", valueID: sprint.ID})
		}
	}
	m.sprints.picker = pickerModel{
		kind:         pickerKindSprintActivate,
		title:        "Pick Planned Sprint To Activate",
		emptyMessage: "No planned sprints available.",
		items:        items,
	}
	m.sprints.modal = sprintModalPicker
}

func (m Model) handleSprintPickerKey(msg tea.KeyMsg) Model {
	picker := &m.sprints.picker
	items := m.filteredPickerItems(*picker)
	m.normalizePickerFocus(picker)
	items = m.filteredPickerItems(*picker)
	switch msg.String() {
	case "esc":
		m.sprints.modal = sprintModalNone
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
		if len(items) == 0 || items[picker.focused].disabled {
			return m
		}
		selected := items[picker.focused]
		switch picker.kind {
		case pickerKindSprintActivate:
			if m.focusSprintByID(selected.valueID) {
				m.sprints.modal = sprintModalNone
				return m.activateFocusedSprint()
			}
		case pickerKindSprintMembership:
			m.sprints.modal = sprintModalNone
			return m.applySprintMembershipAction(picker.ticketID, selected.key)
		case pickerKindSprintCloseTarget:
			m.sprints.close.selectedNextSprint = selected.valueID
			m.sprints.close.mode = sprintCloseUsePlanned
			m.sprints.modal = sprintModalCloseWizard
		}
	case "i", "e":
		m.mode = modeInsert
		picker.editingQuery = true
	default:
		if msg.Type == tea.KeyRunes {
			picker.query += string(msg.Runes)
			m.normalizePickerFocus(picker)
		}
	}
	return m
}

func (m Model) openSprintForm(sprint *kanban.SprintSummary) Model {
	form := sprintFormModel{errors: map[int]string{}}
	if sprint == nil {
		form.title = "Create Sprint"
	} else {
		form.title = "Edit Sprint"
		form.editID = sprint.ID
		form.name = sprint.Name
		form.goal = sprint.Goal
		form.startsOn = sprint.StartsOn.Format("2006-01-02")
		form.endsOn = sprint.EndsOn.Format("2006-01-02")
	}
	m.sprints.form = form
	m.sprints.modal = sprintModalForm
	m.mode = modeNormal
	return m
}

func (m Model) handleSprintFormKey(msg tea.KeyMsg) Model {
	if m.isSprintFormTextField() && msg.Type == tea.KeyRunes && isPrintableRunes(msg.Runes) {
		return m.beginSprintFormEdit(string(msg.Runes))
	}
	switch msg.String() {
	case "j", "down", "tab":
		m.sprints.form.focusedField = min(m.sprints.form.focusedField+1, sprintFormFieldCancel)
	case "k", "up", "shift+tab":
		m.sprints.form.focusedField = max(0, m.sprints.form.focusedField-1)
	case "esc":
		m.sprints.modal = sprintModalNone
		m.mode = modeNormal
	case "i", "e":
		if m.isSprintFormTextField() {
			return m.beginSprintFormEdit("")
		}
	case "enter":
		switch m.sprints.form.focusedField {
		case sprintFormFieldName, sprintFormFieldGoal, sprintFormFieldStartsOn, sprintFormFieldEndsOn:
			return m.beginSprintFormEdit("")
		case sprintFormFieldSubmit:
			return m.submitSprintForm()
		case sprintFormFieldCancel:
			m.sprints.modal = sprintModalNone
		}
	}
	return m
}

func (m Model) renderSprintForm(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("208"))
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(m.sprints.form.title),
		"j/k move, Enter edit/save, Esc cancel",
		"",
	}
	fields := []struct {
		label string
		value string
		err   string
	}{
		{"Name", m.sprintFormFieldValue(sprintFormFieldName), m.sprints.form.errors[sprintFormFieldName]},
		{"Goal", m.sprintFormFieldValue(sprintFormFieldGoal), m.sprints.form.errors[sprintFormFieldGoal]},
		{"Starts On", m.sprintFormFieldValue(sprintFormFieldStartsOn), m.sprints.form.errors[sprintFormFieldStartsOn]},
		{"Ends On", m.sprintFormFieldValue(sprintFormFieldEndsOn), m.sprints.form.errors[sprintFormFieldEndsOn]},
		{"Save", "[Save]", ""},
		{"Cancel", "[Cancel]", ""},
	}
	for idx, field := range fields {
		style := lipgloss.NewStyle()
		prefix := "  "
		if idx == m.sprints.form.focusedField {
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

func (m Model) sprintFormFieldValue(field int) string {
	if m.sprints.form.editingField && field == m.sprints.form.focusedField {
		return m.sprints.form.editingValue
	}
	switch field {
	case sprintFormFieldName:
		return m.sprints.form.name
	case sprintFormFieldGoal:
		if strings.TrimSpace(m.sprints.form.goal) == "" {
			return "(empty)"
		}
		return m.sprints.form.goal
	case sprintFormFieldStartsOn:
		return m.sprints.form.startsOn
	case sprintFormFieldEndsOn:
		return m.sprints.form.endsOn
	default:
		return ""
	}
}

func (m Model) isSprintFormTextField() bool {
	return m.sprints.form.focusedField <= sprintFormFieldEndsOn
}

func (m Model) beginSprintFormEdit(seed string) Model {
	m.mode = modeInsert
	m.sprints.form.editingField = true
	if seed == "" {
		m.sprints.form.editingValue = m.sprintFormFieldValue(m.sprints.form.focusedField)
	} else {
		m.sprints.form.editingValue = seed
	}
	return m
}

func (m Model) commitSprintFormInsert() Model {
	if !m.sprints.form.editingField {
		return m
	}
	switch m.sprints.form.focusedField {
	case sprintFormFieldName:
		m.sprints.form.name = m.sprints.form.editingValue
	case sprintFormFieldGoal:
		m.sprints.form.goal = m.sprints.form.editingValue
	case sprintFormFieldStartsOn:
		m.sprints.form.startsOn = m.sprints.form.editingValue
	case sprintFormFieldEndsOn:
		m.sprints.form.endsOn = m.sprints.form.editingValue
	}
	m.sprints.form.editingField = false
	m.sprints.form.editingValue = ""
	return m
}

func (m Model) submitSprintForm() Model {
	m.sprints.form.errors = map[int]string{}
	name := strings.TrimSpace(m.sprints.form.name)
	if name == "" {
		m.sprints.form.errors[sprintFormFieldName] = "Name is required"
	}
	startsOn, err := parseDate(m.sprints.form.startsOn)
	if err != nil {
		m.sprints.form.errors[sprintFormFieldStartsOn] = err.Error()
	}
	endsOn, err2 := parseDate(m.sprints.form.endsOn)
	if err2 != nil {
		m.sprints.form.errors[sprintFormFieldEndsOn] = err2.Error()
	}
	if err == nil && err2 == nil && endsOn.Before(startsOn) {
		m.sprints.form.errors[sprintFormFieldEndsOn] = "Ends On must be on or after Starts On"
	}
	if len(m.sprints.form.errors) > 0 {
		m.errorMessage = "Fix validation errors"
		return m
	}
	if m.sprints.form.editID == 0 {
		created, err := m.service.CreateSprint(context.Background(), kanban.CreateSprintInput{Name: name, Goal: m.sprints.form.goal, StartsOn: startsOn, EndsOn: endsOn})
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		m.statusMessage = "Created " + created.Name
		m.refreshAllForTicketChange(m.currentSelectedTicketID())
		m.focusSprintByID(created.ID)
	} else {
		current := m.focusedSprintSummary()
		status := kanban.SprintStatusPlanned
		if current != nil && current.ID == m.sprints.form.editID {
			status = current.Status
		} else {
			for _, sprint := range m.sprints.summaries {
				if sprint.ID == m.sprints.form.editID {
					status = sprint.Status
					break
				}
			}
		}
		updated, err := m.service.UpdateSprint(context.Background(), m.sprints.form.editID, kanban.UpdateSprintInput{Name: name, Goal: m.sprints.form.goal, Status: status, StartsOn: startsOn, EndsOn: endsOn})
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		m.statusMessage = "Saved " + updated.Name
		m.refreshAllForTicketChange(m.currentSelectedTicketID())
		m.focusSprintByID(updated.ID)
	}
	m.errorMessage = ""
	m.sprints.modal = sprintModalNone
	return m
}

func (m Model) openCloseSprintWizard() Model {
	sprint := m.focusedSprintSummary()
	if sprint == nil {
		m.errorMessage = "No sprint selected"
		return m
	}
	if sprint.Status != kanban.SprintStatusActive {
		m.errorMessage = "Only the active sprint can be closed"
		return m
	}
	incomplete, err := m.service.ListIncompleteSprintTickets(context.Background(), sprint.ID)
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	planned := m.plannedSprintsExcluding(sprint.ID)
	mode := sprintCloseCreateNew
	selectedNextSprint := int64(0)
	if len(planned) > 0 {
		mode = sprintCloseUsePlanned
		selectedNextSprint = planned[0].ID
	}
	selectedCarryOver := map[string]bool{}
	for _, ticket := range incomplete {
		selectedCarryOver[ticket.TicketID] = true
	}
	startsOn, endsOn := defaultNextSprintDates(sprint.StartsOn, sprint.EndsOn)
	m.sprints.close = sprintCloseWizardModel{
		sprint:             *sprint,
		mode:               mode,
		selectedNextSprint: selectedNextSprint,
		createStartsOn:     startsOn.Format("2006-01-02"),
		createEndsOn:       endsOn.Format("2006-01-02"),
		incompleteTickets:  incomplete,
		selectedCarryOver:  selectedCarryOver,
		errors:             map[string]string{},
	}
	m.sprints.modal = sprintModalCloseWizard
	return m
}

func (m Model) renderCloseSprintWizard(width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("208"))
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render("Close Sprint"),
		fmt.Sprintf("Closing: %s  %s to %s", m.sprints.close.sprint.Name, m.sprints.close.sprint.StartsOn.Format("2006-01-02"), m.sprints.close.sprint.EndsOn.Format("2006-01-02")),
		"",
	}
	modeLabel := "Create New Sprint"
	if m.sprints.close.mode == sprintCloseUsePlanned {
		modeLabel = "Use Planned Sprint"
	}
	lines = append(lines, "Mode: "+modeLabel+" (h/l switch)")
	if m.sprints.close.mode == sprintCloseUsePlanned {
		if len(m.plannedSprintsExcluding(m.sprints.close.sprint.ID)) == 0 {
			lines = append(lines, "No planned sprints available.")
		} else {
			for _, sprint := range m.plannedSprintsExcluding(m.sprints.close.sprint.ID) {
				prefix := "  "
				if sprint.ID == m.sprints.close.selectedNextSprint {
					prefix = "> "
				}
				lines = append(lines, fmt.Sprintf("%s%s", prefix, sprint.Name))
			}
		}
	} else {
		lines = append(lines, "Name: "+m.sprints.close.createName)
		lines = append(lines, "Goal: "+displayEmpty(m.sprints.close.createGoal))
		lines = append(lines, "Starts On: "+m.sprints.close.createStartsOn)
		lines = append(lines, "Ends On: "+m.sprints.close.createEndsOn)
	}
	lines = append(lines, "", "Carry Over Incomplete Tickets (space toggles):")
	if len(m.sprints.close.incompleteTickets) == 0 {
		lines = append(lines, "No incomplete tickets. Sprint can be closed immediately.")
	} else {
		for idx, ticket := range m.sprints.close.incompleteTickets {
			marker := "[ ]"
			if m.sprints.close.selectedCarryOver[ticket.TicketID] {
				marker = "[x]"
			}
			prefix := "  "
			if idx == m.sprints.close.focusedTicket {
				prefix = "> "
			}
			lines = append(lines, fmt.Sprintf("%s%s %s %s", prefix, marker, ticket.TicketID, ticket.Title))
		}
	}
	lines = append(lines, "", "Enter confirm  Esc cancel")
	if msg := m.sprints.close.errors["submit"]; msg != "" {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(msg))
	}
	return box.Render(strings.Join(lines, "\n"))
}

func (m Model) handleSprintCloseWizardKey(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "esc":
		m.sprints.modal = sprintModalNone
	case "h", "left":
		if len(m.plannedSprintsExcluding(m.sprints.close.sprint.ID)) > 0 {
			m.sprints.close.mode = sprintCloseUsePlanned
		}
	case "l", "right":
		m.sprints.close.mode = sprintCloseCreateNew
	case "j", "down":
		if m.sprints.close.focusedTicket < len(m.sprints.close.incompleteTickets)-1 {
			m.sprints.close.focusedTicket++
		}
	case "k", "up":
		if m.sprints.close.focusedTicket > 0 {
			m.sprints.close.focusedTicket--
		}
	case " ":
		if len(m.sprints.close.incompleteTickets) > 0 {
			ticket := m.sprints.close.incompleteTickets[m.sprints.close.focusedTicket]
			m.sprints.close.selectedCarryOver[ticket.TicketID] = !m.sprints.close.selectedCarryOver[ticket.TicketID]
		}
	case "e", "i":
		if m.sprints.close.mode == sprintCloseCreateNew {
			m.openSprintCloseTargetFormPicker()
		}
	case "enter":
		if m.sprints.close.mode == sprintCloseUsePlanned && len(m.plannedSprintsExcluding(m.sprints.close.sprint.ID)) > 0 && m.sprints.close.selectedNextSprint == 0 {
			m.sprints.close.selectedNextSprint = m.plannedSprintsExcluding(m.sprints.close.sprint.ID)[0].ID
		}
		return m.submitCloseSprintWizard()
	}
	return m
}

func (m *Model) openSprintCloseTargetFormPicker() {
	items := []pickerItem{
		{key: "name", label: "Edit Name"},
		{key: "goal", label: "Edit Goal"},
		{key: "starts_on", label: "Edit Starts On"},
		{key: "ends_on", label: "Edit Ends On"},
	}
	m.sprints.picker = pickerModel{kind: pickerKindSprintCloseTarget, title: "Close Wizard Fields", items: items}
	m.sprints.modal = sprintModalPicker
}

func (m Model) submitCloseSprintWizard() Model {
	input := kanban.CloseSprintInput{}
	if m.sprints.close.mode == sprintCloseUsePlanned {
		if m.sprints.close.selectedNextSprint == 0 {
			m.errorMessage = "Choose a planned sprint or create a new one"
			return m
		}
		input.NextSprintID = &m.sprints.close.selectedNextSprint
	} else {
		startsOn, err := parseDate(m.sprints.close.createStartsOn)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		endsOn, err := parseDate(m.sprints.close.createEndsOn)
		if err != nil {
			m.errorMessage = err.Error()
			return m
		}
		if endsOn.Before(startsOn) {
			m.errorMessage = "Ends On must be on or after Starts On"
			return m
		}
		input.CreateNextSprint = &kanban.CreateSprintInput{
			Name:     strings.TrimSpace(m.sprints.close.createName),
			Goal:     m.sprints.close.createGoal,
			StartsOn: startsOn,
			EndsOn:   endsOn,
		}
	}
	for _, ticket := range m.sprints.close.incompleteTickets {
		if m.sprints.close.selectedCarryOver[ticket.TicketID] {
			input.CarryOverTicketIDs = append(input.CarryOverTicketIDs, ticket.TicketID)
		}
	}
	closed, err := m.service.CloseSprint(context.Background(), m.sprints.close.sprint.ID, input)
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	m.errorMessage = ""
	m.statusMessage = "Closed " + closed.Name
	m.sprints.modal = sprintModalNone
	m.refreshAllForTicketChange(m.currentSelectedTicketID())
	if !m.focusSprintByStatus(kanban.SprintStatusActive) {
		m.focusSprintByID(closed.ID)
	}
	return m
}

func defaultNextSprintDates(start, end time.Time) (time.Time, time.Time) {
	duration := int(end.Sub(start).Hours()/24) + 1
	nextStart := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location()).AddDate(0, 0, 1)
	nextEnd := nextStart.AddDate(0, 0, duration-1)
	return nextStart, nextEnd
}

func (m Model) plannedSprintsExcluding(excludeID int64) []kanban.SprintSummary {
	out := []kanban.SprintSummary{}
	for _, sprint := range m.sprints.summaries {
		if sprint.Status == kanban.SprintStatusPlanned && sprint.ID != excludeID {
			out = append(out, sprint)
		}
	}
	return out
}

func (m *Model) refreshAllForTicketChange(preferTicketID string) {
	m.refreshSprints()
	m.refreshEpics()
	m.refreshSupportData()
	m.refreshBoard(preferTicketID)
	m.refreshTickets()
	if preferTicketID != "" {
		m.reloadSelectedTicket(preferTicketID)
	}
}

func (m Model) applySprintMembershipAction(ticketID, action string) Model {
	if ticketID == "" {
		m.errorMessage = "Select a ticket first"
		return m
	}
	var (
		detail kanban.TicketDetail
		err    error
	)
	switch action {
	case "add":
		detail, err = m.service.AddTicketToActiveSprint(context.Background(), ticketID)
	case "remove":
		detail, err = m.service.RemoveTicketFromActiveSprint(context.Background(), ticketID)
	default:
		return m
	}
	if err != nil {
		m.errorMessage = err.Error()
		return m
	}
	m.statusMessage = "Updated sprint for " + detail.TicketID
	m.errorMessage = ""
	m.selectedTicket = &detail
	m.refreshAllForTicketChange(detail.TicketID)
	return m
}

func (m *Model) openSprintMembershipPicker(ticketID string) {
	active, err := m.service.GetActiveSprint(context.Background())
	if err != nil || active == nil {
		m.sprints.picker = pickerModel{
			kind:         pickerKindSprintMembership,
			title:        "Sprint Membership",
			emptyMessage: "No active sprint exists. Activate or create a sprint first.",
			ticketID:     ticketID,
		}
		m.sprints.modal = sprintModalPicker
		return
	}
	selected := m.selectedTicket
	inActive := selected != nil && selected.CurrentSprintID != nil && *selected.CurrentSprintID == active.ID
	item := pickerItem{key: "add", label: "Add to active sprint: " + active.Name}
	if inActive {
		item = pickerItem{key: "remove", label: "Remove from active sprint: " + active.Name}
	}
	m.sprints.picker = pickerModel{
		kind:         pickerKindSprintMembership,
		title:        "Sprint Membership",
		emptyMessage: "No sprint action available.",
		ticketID:     ticketID,
		items:        []pickerItem{item},
	}
	m.sprints.modal = sprintModalPicker
}

func parseDate(value string) (time.Time, error) {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, fmt.Errorf("dates must use YYYY-MM-DD")
	}
	return parsed, nil
}

func displayEmpty(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(empty)"
	}
	return value
}
