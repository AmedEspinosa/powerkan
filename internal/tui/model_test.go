package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amedespinosa/powerkan/internal/config"
	"github.com/amedespinosa/powerkan/internal/kanban"
	"github.com/amedespinosa/powerkan/internal/platform"
	"github.com/amedespinosa/powerkan/internal/storage"
)

func newTestModel(t *testing.T) Model {
	t.Helper()

	cfg := config.Defaults(t.TempDir())
	cfg.App.Timezone = "America/New_York"
	db, err := storage.Open(context.Background(), t.TempDir()+"/kanban.db")
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyMigrations returned error: %v", err)
	}

	service := kanban.NewService(db, cfg)
	baseNow := time.Date(2026, 4, 24, 10, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	var tick int
	service.SetNowFunc(func() time.Time {
		current := baseNow.Add(time.Duration(tick) * time.Second)
		tick++
		return current
	})

	epic, err := service.CreateEpic(context.Background(), kanban.CreateEpicInput{Title: "Freedom Tower"})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	sprint, err := service.CreateSprint(context.Background(), kanban.CreateSprintInput{
		Name:     "26Q2 Sprint 3",
		Goal:     "Ship TUI",
		Status:   kanban.SprintStatusActive,
		StartsOn: time.Date(2026, 4, 20, 0, 0, 0, 0, time.Local),
		EndsOn:   time.Date(2026, 4, 26, 0, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateSprint returned error: %v", err)
	}
	create := func(title string, status kanban.TicketStatus, blocked bool, points float64) {
		_, err := service.CreateTicket(context.Background(), kanban.CreateTicketInput{
			Title:       title,
			Status:      status,
			Type:        kanban.TicketTypeFeature,
			Blocked:     blocked,
			StoryPoints: points,
			EpicID:      &epic.ID,
			SprintID:    &sprint.ID,
			Description: "description for " + title,
			GitHubPRURL: "https://example.com/pr",
		})
		if err != nil {
			t.Fatalf("CreateTicket returned error: %v", err)
		}
	}
	create("Interactive Backend Design", kanban.TicketStatusNotStarted, false, 1)
	create("Interactive Backend Implementation", kanban.TicketStatusInProgress, true, 2)
	create("Review API Contract", kanban.TicketStatusUnderReview, true, 3)
	create("Ship Sprint Demo", kanban.TicketStatusDone, false, 5)

	return NewModel(Dependencies{
		Config:  cfg,
		Paths:   platform.Paths{},
		Service: service,
	})
}

func updateModel(t *testing.T, model Model, msg tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(msg)
	return updated.(Model)
}

func TestNewModelStartsOnBoard(t *testing.T) {
	t.Parallel()
	model := newTestModel(t)
	if model.activeRoute != routeBoard {
		t.Fatalf("expected default route to be board, got %v", model.activeRoute)
	}
}

func TestNumericRouteSwitchingAndDetailOpen(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if model.activeRoute != routeTickets {
		t.Fatalf("expected tickets route, got %v", model.activeRoute)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.activeRoute != routeTicketDetail {
		t.Fatalf("expected detail route, got %v", model.activeRoute)
	}
}

func TestLetterRouteSwitching(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	if model.activeRoute != routeSprints {
		t.Fatalf("expected sprints route, got %v", model.activeRoute)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	if model.activeRoute != routeEpics {
		t.Fatalf("expected epics route, got %v", model.activeRoute)
	}
}

func TestBoardViewContainsStatuses(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 160, Height: 40})
	view := model.View()
	for _, status := range []string{"Not Started", "In Progress", "Under Review", "Completed"} {
		if !strings.Contains(view, status) {
			t.Fatalf("expected board view to contain %q", status)
		}
	}
}

func TestCreateTicketFromTicketsReturnsSelection(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	model = model.openCreateTicket(routeTickets, nil)
	model.create.title = "Created from tickets"
	model.create.storyPoints = "5"
	model.create.description = "new ticket"
	model.create.epicID = model.tickets.data.Epics[0].ID
	model.create.epicName = model.tickets.data.Epics[0].Title
	model = model.submitCreateTicket()

	if model.activeRoute != routeTickets {
		t.Fatalf("expected to return to tickets, got %v", model.activeRoute)
	}
	if model.selectedTicket == nil || model.selectedTicket.Title != "Created from tickets" {
		t.Fatalf("expected selected created ticket, got %+v", model.selectedTicket)
	}
}

func TestSprintsRouteRendersGroupedListInsteadOfPlaceholder(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'2'}})
	model = updateModel(t, model, tea.WindowSizeMsg{Width: 140, Height: 40})
	view := model.View()
	for _, text := range []string{"Sprints", "Active", "Planned", "Closed"} {
		if !strings.Contains(view, text) {
			t.Fatalf("expected sprints view to contain %q", text)
		}
	}
}

func TestSprintCreateAndEditOpenFromRouteKeys(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	_, err := model.service.CreateSprint(context.Background(), kanban.CreateSprintInput{
		Name:     "26Q2 Sprint 4",
		Goal:     "Wrap up",
		Status:   kanban.SprintStatusPlanned,
		StartsOn: time.Date(2026, 4, 27, 0, 0, 0, 0, time.Local),
		EndsOn:   time.Date(2026, 5, 3, 0, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateSprint returned error: %v", err)
	}
	model.refreshSprints()
	model.activeRoute = routeSprints

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if model.sprints.modal != sprintModalForm || model.sprints.form.editID != 0 {
		t.Fatalf("expected create sprint form, got %+v", model.sprints.form)
	}

	model.sprints.modal = sprintModalNone
	model.focusSprintByStatus(kanban.SprintStatusPlanned)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if model.sprints.modal != sprintModalForm || model.sprints.form.editID == 0 {
		t.Fatalf("expected edit sprint form, got %+v", model.sprints.form)
	}
}

func TestSprintActivateAndCloseWizardFlows(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	planned, err := model.service.CreateSprint(context.Background(), kanban.CreateSprintInput{
		Name:     "26Q2 Sprint 4",
		Goal:     "Next up",
		Status:   kanban.SprintStatusPlanned,
		StartsOn: time.Date(2026, 4, 27, 0, 0, 0, 0, time.Local),
		EndsOn:   time.Date(2026, 5, 3, 0, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateSprint returned error: %v", err)
	}
	model.refreshSprints()
	model.activeRoute = routeSprints
	model.focusSprintByID(planned.ID)

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if active, err := model.service.GetActiveSprint(context.Background()); err == nil && active.ID == planned.ID {
		t.Fatalf("expected activate to fail while another sprint is active")
	}
	if model.errorMessage == "" {
		t.Fatalf("expected activation error message")
	}

	activeID := model.board.data.Metrics.Sprint.ID
	model.refreshSprints()
	model.focusSprintByID(activeID)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if model.sprints.modal != sprintModalCloseWizard {
		t.Fatalf("expected close wizard modal, got %v", model.sprints.modal)
	}
}

func TestPaletteEpicCommandsRequireSelectedTicket(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model.selectedTicket = nil
	model = model.executePaletteCommand("epic.set_for_ticket")
	if model.errorMessage == "" {
		t.Fatalf("expected missing ticket error for epic.set_for_ticket")
	}
	model.errorMessage = ""
	model = model.executePaletteCommand("epic.clear_for_ticket")
	if model.errorMessage == "" {
		t.Fatalf("expected missing ticket error for epic.clear_for_ticket")
	}
}

func TestBoardSprintMembershipPickerOpens(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model.activeRoute = routeBoard
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'m'}})
	if model.activeRoute != routeSprints {
		t.Fatalf("expected sprint route after board membership action, got %v", model.activeRoute)
	}
	if model.sprints.modal != sprintModalPicker || model.sprints.picker.kind != pickerKindSprintMembership {
		t.Fatalf("expected sprint membership picker, got %+v", model.sprints.picker)
	}
}

func TestDetailEpicAndSprintFieldsOpenPickers(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	selected := model.selectedBoardTicket()
	if selected == nil {
		t.Fatal("expected selected board ticket")
	}
	model = model.openDetail(selected.TicketID)

	model.detail.focusedField = 4
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if model.activeRoute != routeEpics || model.epics.modal != epicModalPicker {
		t.Fatalf("expected epic picker from detail epic field")
	}

	model.activeRoute = routeTicketDetail
	model.epics.modal = epicModalNone
	model.detail.focusedField = 6
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if model.activeRoute != routeSprints || model.sprints.modal != sprintModalPicker {
		t.Fatalf("expected sprint picker from detail sprint field")
	}
}

func TestTicketsEpicAndSprintColumnsAreReadOnly(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model.activeRoute = routeTickets
	model.tickets.focusedColumn = 2
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if model.tickets.editingCell {
		t.Fatal("expected epic column to remain read-only")
	}
	if !strings.Contains(model.statusMessage, "read-only") {
		t.Fatalf("expected read-only status message, got %q", model.statusMessage)
	}

	model.statusMessage = ""
	model.tickets.focusedColumn = 5
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if model.tickets.editingCell {
		t.Fatal("expected sprint column to remain read-only")
	}
	if !strings.Contains(model.statusMessage, "read-only") {
		t.Fatalf("expected read-only status message, got %q", model.statusMessage)
	}
}
