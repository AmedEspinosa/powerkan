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

	epic, err := service.CreateEpic(context.Background(), kanban.CreateEpicInput{Name: "Freedom Tower"})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	sprint, err := service.CreateSprint(context.Background(), kanban.CreateSprintInput{
		Name:      "26Q2 Sprint 3",
		Quarter:   "2026Q2",
		StartDate: time.Date(2026, 4, 20, 0, 0, 0, 0, time.Local),
		EndDate:   time.Date(2026, 4, 26, 0, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateSprint returned error: %v", err)
	}
	create := func(title string, status kanban.TicketStatus, blocked bool, points int) kanban.TicketDetail {
		ticket, err := service.CreateTicket(context.Background(), kanban.CreateTicketInput{
			Title:       title,
			Status:      status,
			Type:        kanban.TicketTypeFeature,
			Blocked:     blocked,
			StoryPoints: points,
			EpicID:      epic.ID,
			SprintID:    &sprint.ID,
			Description: "description for " + title,
			GitHubPRURL: "https://example.com/pr",
		})
		if err != nil {
			t.Fatalf("CreateTicket returned error: %v", err)
		}
		return ticket
	}
	first := create("Interactive Backend Design", kanban.TicketStatusNotStarted, false, 1)
	_ = create("Interactive Backend Implementation", kanban.TicketStatusInProgress, true, 2)
	_ = create("Review API Contract", kanban.TicketStatusUnderReview, true, 3)
	_ = create("Ship Sprint Demo", kanban.TicketStatusDone, false, 5)
	if _, err := service.AddComment(context.Background(), first.TicketID, kanban.AddCommentInput{Kind: kanban.CommentKindText, Body: "comment one"}); err != nil {
		t.Fatalf("AddComment returned error: %v", err)
	}

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

func TestModelNumericRouteSwitching(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if model.activeRoute != routeTickets {
		t.Fatalf("expected tickets route, got %v", model.activeRoute)
	}
}

func TestBoardFocusRespectsColumnBounds(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	for range len(boardStatuses) + 2 {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	}
	if model.board.focusedColumn != len(boardStatuses)-1 {
		t.Fatalf("expected focused last column, got %d", model.board.focusedColumn)
	}
	for range len(boardStatuses) + 2 {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyLeft})
	}
	if model.board.focusedColumn != 0 {
		t.Fatalf("expected focused first column, got %d", model.board.focusedColumn)
	}
}

func TestBoardViewContainsAllStatuses(t *testing.T) {
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

func TestBoardSearchInsertModeConsumesNavigationCharacters(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if model.mode != modeInsert {
		t.Fatalf("expected insert mode, got %v", model.mode)
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
	if model.board.searchQuery != "jh" {
		t.Fatalf("expected search query jh, got %q", model.board.searchQuery)
	}
	if model.board.focusedColumn != 0 {
		t.Fatalf("expected focus unchanged, got %d", model.board.focusedColumn)
	}
}

func TestBoardMoveTicketUpdatesSelectionAndStatus(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	selected := model.selectedBoardTicket()
	if selected == nil {
		t.Fatal("expected selected board ticket")
	}

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	selected = model.selectedBoardTicket()
	if selected == nil {
		t.Fatal("expected selected ticket after move")
	}
	if selected.Status != kanban.TicketStatusInProgress {
		t.Fatalf("expected moved ticket in progress, got %s", selected.Status)
	}
	if model.board.focusedColumn != 1 {
		t.Fatalf("expected focused second column, got %d", model.board.focusedColumn)
	}
}

func TestTableEnterOpensDetailForFocusedTicket(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.activeRoute != routeTicketDetail {
		t.Fatalf("expected ticket detail route, got %v", model.activeRoute)
	}
	if model.selectedTicket == nil {
		t.Fatal("expected selected ticket in detail route")
	}
}

func TestFilterScopeDoesNotAffectTicketsList(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	initialTicketCount := len(model.tickets.data.Tickets)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	if len(model.tickets.data.Tickets) != initialTicketCount {
		t.Fatalf("expected tickets list to remain unfiltered, got %d", len(model.tickets.data.Tickets))
	}
}

func TestFooterHelpMatchesCurrentBoardBindings(t *testing.T) {
	t.Parallel()

	view := renderFooter(120, routeBoard, modeNormal, "", "")
	if !strings.Contains(view, "h/l columns") {
		t.Fatalf("expected board footer bindings, got %q", view)
	}
}
