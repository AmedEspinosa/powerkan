package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/amedespinosa/powerkan/internal/config"
	"github.com/amedespinosa/powerkan/internal/platform"
)

func TestNewModelStartsOnBoard(t *testing.T) {
	t.Parallel()

	model := NewModel(config.Defaults("/tmp/exports"), platform.Paths{})
	if model.activeRoute != routeBoard {
		t.Fatalf("expected default route to be board, got %v", model.activeRoute)
	}
}

func TestModelRouteSwitching(t *testing.T) {
	t.Parallel()

	model := NewModel(config.Defaults("/tmp/exports"), platform.Paths{})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
	nextModel := updated.(Model)

	if nextModel.activeRoute != routeSprints {
		t.Fatalf("expected sprints route, got %v", nextModel.activeRoute)
	}
}

func TestBoardViewContainsAllStatuses(t *testing.T) {
	t.Parallel()

	model := NewModel(config.Defaults("/tmp/exports"), platform.Paths{})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	view := updated.(Model).View()

	for _, status := range boardStatuses {
		if !strings.Contains(view, status) {
			t.Fatalf("expected board view to contain %q", status)
		}
	}
}
