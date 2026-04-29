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

func TestQuitKeyReturnsTeaQuitCommand(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if _, ok := updated.(Model); !ok {
		t.Fatalf("expected updated model type, got %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected quit command, got nil")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Fatalf("expected tea.Quit message, got %T", msg)
	}
}

func TestInsertModeQAppendsInsteadOfQuitting(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = updated.(Model)
	if cmd != nil {
		t.Fatal("expected no quit command in insert mode")
	}
	if model.board.searchQuery != "q" {
		t.Fatalf("expected insert mode to append q, got %q", model.board.searchQuery)
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

func TestDetailEnterPreservesBackNavigation(t *testing.T) {
	t.Parallel()

	t.Run("board", func(t *testing.T) {
		model := newTestModel(t)
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
		if model.activeRoute != routeTicketDetail {
			t.Fatalf("expected detail route, got %v", model.activeRoute)
		}
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
		if model.activeRoute != routeBoard {
			t.Fatalf("expected escape to return to board, got %v", model.activeRoute)
		}
	})

	t.Run("tickets", func(t *testing.T) {
		model := newTestModel(t)
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
		if model.activeRoute != routeTicketDetail {
			t.Fatalf("expected detail route, got %v", model.activeRoute)
		}
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
		if model.activeRoute != routeTickets {
			t.Fatalf("expected escape to return to tickets, got %v", model.activeRoute)
		}
	})
}

func TestRenderSprintPanelShowsStoredPercentValue(t *testing.T) {
	t.Parallel()

	view := renderSprintPanel(48, 12, kanban.BoardMetrics{PercentCompleted: 50})
	if !strings.Contains(view, "% of Points Completed: 50.00%") {
		t.Fatalf("expected rendered sprint percent, got %q", view)
	}
	if strings.Contains(view, "5000.00%") {
		t.Fatalf("expected no double-scaled percent, got %q", view)
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

func TestBoardSearchEscRestoresOriginalQuery(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	for _, r := range "backend" {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})
	if model.board.searchQuery != "backend" {
		t.Fatalf("expected committed query backend, got %q", model.board.searchQuery)
	}

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	for _, r := range " ship" {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})

	if model.board.searchQuery != "backend" {
		t.Fatalf("expected escape to restore backend query, got %q", model.board.searchQuery)
	}
	if model.mode != modeNormal {
		t.Fatalf("expected normal mode after escape, got %v", model.mode)
	}
}

func TestBoardSearchTypingKeepsSelectionValidWhenFilteredSetShrinks(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	if selected := model.selectedBoardTicket(); selected == nil || selected.TicketID == "" {
		t.Fatal("expected ticket selected before filtering")
	}

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	for _, r := range "ship" {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	selected := model.selectedBoardTicket()
	if selected == nil {
		t.Fatal("expected selected board ticket after filtering")
	}
	if selected.Title != "Ship Sprint Demo" {
		t.Fatalf("expected filtered selection to move to Ship Sprint Demo, got %s", selected.Title)
	}
	if model.selectedTicket == nil || model.selectedTicket.TicketID != selected.TicketID {
		t.Fatalf("expected selected ticket detail to match focused card, got %+v", model.selectedTicket)
	}
	if model.board.focusedColumn != 3 {
		t.Fatalf("expected focus to clamp to done column, got %d", model.board.focusedColumn)
	}
}

func TestBoardSearchCommitKeepsSelectionClamped(t *testing.T) {
	t.Parallel()

	model := newTestModel(t)
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	for _, r := range "ship" {
		model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEnter})

	selected := model.selectedBoardTicket()
	if selected == nil || selected.Title != "Ship Sprint Demo" {
		t.Fatalf("expected selected board ticket Ship Sprint Demo after commit, got %+v", selected)
	}
	if model.selectedTicket == nil || model.selectedTicket.Title != "Ship Sprint Demo" {
		t.Fatalf("expected selected detail Ship Sprint Demo after commit, got %+v", model.selectedTicket)
	}
	if model.mode != modeNormal {
		t.Fatalf("expected normal mode after commit, got %v", model.mode)
	}
}

func TestTicketsNavigationOnlyFetchesDetailOnRowChange(t *testing.T) {
	t.Parallel()

	service := newCountingService()
	model := NewModel(Dependencies{Config: config.Config{}, Paths: platform.Paths{}, Service: service})
	initialCalls := service.getTicketDetailCalls

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRight})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'i'}})
	if service.getTicketDetailCalls != initialCalls {
		t.Fatalf("expected no detail fetch on column move or edit entry, got %d want %d", service.getTicketDetailCalls, initialCalls)
	}

	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyEsc})
	model = updateModel(t, model, tea.KeyMsg{Type: tea.KeyDown})
	if service.getTicketDetailCalls != initialCalls+1 {
		t.Fatalf("expected one detail fetch on row change, got %d want %d", service.getTicketDetailCalls, initialCalls+1)
	}
}

func TestNewModelUsesListTicketsAsSupportDataSource(t *testing.T) {
	t.Parallel()

	service := newCountingService()
	model := NewModel(Dependencies{Config: config.Config{}, Paths: platform.Paths{}, Service: service})

	if len(model.tickets.data.Epics) == 0 || len(model.tickets.data.Sprints) == 0 {
		t.Fatalf("expected support data from ListTickets, got %+v", model.tickets.data)
	}
	if service.listTicketsCalls != 1 {
		t.Fatalf("expected one ListTickets call, got %d", service.listTicketsCalls)
	}
	if service.listEpicsCalls != 0 {
		t.Fatalf("expected no ListEpics call, got %d", service.listEpicsCalls)
	}
	if service.listSprintsCalls != 0 {
		t.Fatalf("expected no ListSprints call, got %d", service.listSprintsCalls)
	}
}

func TestRenderedLabelsUseGitHubSpelling(t *testing.T) {
	t.Parallel()

	ticket := &kanban.TicketDetail{Ticket: kanban.Ticket{
		TicketID:    "TICKET-001",
		Title:       "Title",
		Description: "Description",
		EpicName:    "Epic",
		Type:        kanban.TicketTypeFeature,
		GitHubPRURL: "https://example.com/pr",
	}}
	panel := renderSelectedTicketPanel(60, 20, ticket)
	if strings.Contains(panel, "Github") {
		t.Fatalf("expected GitHub spelling in board panel, got %q", panel)
	}
	if !strings.Contains(panel, "GitHub PR: https://example.com/pr") {
		t.Fatalf("expected GitHub label in board panel, got %q", panel)
	}
	if strings.Contains(strings.Join(ticketTableColumns, "\n"), "Github") {
		t.Fatalf("expected GitHub spelling in ticket table headers, got %v", ticketTableColumns)
	}
	if strings.Contains(strings.Join(detailFields, "\n"), "Github") {
		t.Fatalf("expected GitHub spelling in detail fields, got %v", detailFields)
	}
}

type countingService struct {
	boardData            kanban.BoardData
	ticketList           kanban.TicketListResult
	details              map[string]kanban.TicketDetail
	getTicketDetailCalls int
	listTicketsCalls     int
	listEpicsCalls       int
	listSprintsCalls     int
}

func newCountingService() *countingService {
	ticket1 := kanban.Ticket{
		ID:          1,
		TicketID:    "TICKET-001",
		Title:       "One",
		Status:      kanban.TicketStatusNotStarted,
		Type:        kanban.TicketTypeFeature,
		EpicID:      1,
		EpicName:    "Epic One",
		GitHubPRURL: "https://example.com/1",
	}
	ticket2 := kanban.Ticket{
		ID:          2,
		TicketID:    "TICKET-002",
		Title:       "Two",
		Status:      kanban.TicketStatusInProgress,
		Type:        kanban.TicketTypeBug,
		EpicID:      1,
		EpicName:    "Epic One",
		GitHubPRURL: "https://example.com/2",
	}
	columns := make([]kanban.BoardColumn, 0, len(boardStatuses))
	for _, status := range boardStatuses {
		column := kanban.BoardColumn{Status: status}
		switch status {
		case kanban.TicketStatusNotStarted:
			column.Tickets = []kanban.Ticket{ticket1}
		case kanban.TicketStatusInProgress:
			column.Tickets = []kanban.Ticket{ticket2}
		}
		columns = append(columns, column)
	}
	return &countingService{
		boardData: kanban.BoardData{Columns: columns},
		ticketList: kanban.TicketListResult{
			Tickets: []kanban.Ticket{ticket1, ticket2},
			Epics:   []kanban.Epic{{ID: 1, Name: "Epic One"}},
			Sprints: []kanban.Sprint{{ID: 1, Name: "Sprint One"}},
		},
		details: map[string]kanban.TicketDetail{
			ticket1.TicketID: {Ticket: ticket1},
			ticket2.TicketID: {Ticket: ticket2},
		},
	}
}

func (s *countingService) LoadBoard(context.Context) (kanban.BoardData, error) {
	return s.boardData, nil
}

func (s *countingService) ListTickets(context.Context, kanban.TicketListFilters) (kanban.TicketListResult, error) {
	s.listTicketsCalls++
	return s.ticketList, nil
}

func (s *countingService) GetTicketDetail(_ context.Context, ticketID string) (kanban.TicketDetail, error) {
	s.getTicketDetailCalls++
	return s.details[ticketID], nil
}

func (s *countingService) UpdateTicket(context.Context, string, kanban.UpdateTicketInput) (kanban.TicketDetail, error) {
	return kanban.TicketDetail{}, nil
}

func (s *countingService) MoveTicket(context.Context, string, int) (kanban.TicketDetail, error) {
	return kanban.TicketDetail{}, nil
}

func (s *countingService) AddComment(context.Context, string, kanban.AddCommentInput) (kanban.TicketComment, error) {
	return kanban.TicketComment{}, nil
}

func (s *countingService) ListEpics(context.Context) ([]kanban.Epic, error) {
	s.listEpicsCalls++
	return s.ticketList.Epics, nil
}

func (s *countingService) ListSprints(context.Context, kanban.SprintListFilters) ([]kanban.SprintSummary, error) {
	s.listSprintsCalls++
	return nil, nil
}

func (s *countingService) ExportTicketMarkdown(context.Context, string, string) (string, error) {
	return "", nil
}

func (s *countingService) ExportTicketCSV(context.Context, string, string) (string, error) {
	return "", nil
}
