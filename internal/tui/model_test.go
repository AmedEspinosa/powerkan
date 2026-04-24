package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amedespinosa/powerkan/internal/config"
	"github.com/amedespinosa/powerkan/internal/platform"
)

func newTestModel() Model {
	return NewModel(config.Defaults("/tmp/exports"), platform.Paths{})
}

func TestNewModelStartsOnBoard(t *testing.T) {
	t.Parallel()

	model := newTestModel()
	if model.activeRoute != routeBoard {
		t.Fatalf("expected default route to be board, got %v", model.activeRoute)
	}
}

func TestModelNumericRouteSwitching(t *testing.T) {
	t.Parallel()

	model := newTestModel()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	nextModel := updated.(Model)

	if nextModel.activeRoute != routeSprints {
		t.Fatalf("expected sprints route, got %v", nextModel.activeRoute)
	}
}

func TestModelUppercaseRouteSwitching(t *testing.T) {
	t.Parallel()

	model := newTestModel()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'L'}})
	nextModel := updated.(Model)

	if nextModel.activeRoute != routeTickets {
		t.Fatalf("expected tickets route after L, got %v", nextModel.activeRoute)
	}

	updated, _ = nextModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'H'}})
	previousModel := updated.(Model)
	if previousModel.activeRoute != routeBoard {
		t.Fatalf("expected board route after H, got %v", previousModel.activeRoute)
	}
}

func TestBoardArrowKeysMoveBoardFocusWithoutChangingRoute(t *testing.T) {
	t.Parallel()

	model := newTestModel()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
	nextModel := updated.(Model)

	if nextModel.activeRoute != routeBoard {
		t.Fatalf("expected to remain on board route, got %v", nextModel.activeRoute)
	}
	if nextModel.board.focusedColumn != 1 {
		t.Fatalf("expected focused column 1 after right arrow, got %d", nextModel.board.focusedColumn)
	}

	updated, _ = nextModel.Update(tea.KeyMsg{Type: tea.KeyLeft})
	nextModel = updated.(Model)
	if nextModel.board.focusedColumn != 0 {
		t.Fatalf("expected focused column 0 after left arrow, got %d", nextModel.board.focusedColumn)
	}
}

func TestBoardFocusRespectsColumnBounds(t *testing.T) {
	t.Parallel()

	model := newTestModel()
	for range len(boardStatuses) + 2 {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRight})
		model = updated.(Model)
	}

	if model.board.focusedColumn != len(boardStatuses)-1 {
		t.Fatalf("expected focus to clamp at last column, got %d", model.board.focusedColumn)
	}

	for range len(boardStatuses) + 2 {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyLeft})
		model = updated.(Model)
	}

	if model.board.focusedColumn != 0 {
		t.Fatalf("expected focus to clamp at first column, got %d", model.board.focusedColumn)
	}
}

func TestBoardViewContainsAllStatuses(t *testing.T) {
	t.Parallel()

	model := newTestModel()
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := updated.(Model).View()

	for _, status := range boardStatuses {
		if !strings.Contains(view, status) {
			t.Fatalf("expected board view to contain %q", status)
		}
	}
}

func TestFooterHelpMatchesCurrentBoardBindings(t *testing.T) {
	t.Parallel()

	view := renderFooter(120, routeBoard)
	if strings.Contains(view, "j/k") {
		t.Fatalf("expected board footer not to advertise j/k, got %q", view)
	}
	if !strings.Contains(view, "left/right") {
		t.Fatalf("expected board footer to advertise arrow navigation, got %q", view)
	}
}
