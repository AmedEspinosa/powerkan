package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m *Model) normalizePickerFocus(picker *pickerModel) {
	items := m.filteredPickerItems(*picker)
	if len(items) == 0 {
		picker.focused = 0
		return
	}
	if picker.focused >= len(items) {
		picker.focused = len(items) - 1
	}
	if picker.focused < 0 {
		picker.focused = 0
	}
}

func (m Model) filteredPickerItems(picker pickerModel) []pickerItem {
	query := strings.ToLower(strings.TrimSpace(picker.query))
	if query == "" {
		return picker.items
	}
	items := make([]pickerItem, 0, len(picker.items))
	for _, item := range picker.items {
		haystack := strings.ToLower(item.label + " " + item.description)
		if strings.Contains(haystack, query) {
			items = append(items, item)
		}
	}
	return items
}

func (m Model) renderPicker(picker pickerModel, width, height int) string {
	box := lipgloss.NewStyle().Width(width).Height(height).Padding(1).Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("208"))
	lines := []string{
		lipgloss.NewStyle().Bold(true).Render(picker.title),
		"type to search, j/k move, Enter select, Esc close",
		"Query: " + picker.query,
		"",
	}
	items := m.filteredPickerItems(picker)
	if len(items) == 0 {
		lines = append(lines, picker.emptyMessage)
		return box.Render(strings.Join(lines, "\n"))
	}
	for idx, item := range items {
		prefix := "  "
		style := lipgloss.NewStyle()
		if idx == picker.focused {
			prefix = "> "
			style = style.Foreground(lipgloss.Color("208"))
		}
		label := item.label
		if item.description != "" {
			label = fmt.Sprintf("%s [%s]", item.label, item.description)
		}
		if item.disabled {
			style = style.Foreground(lipgloss.Color("245"))
		}
		lines = append(lines, style.Render(prefix+label))
	}
	return box.Render(strings.Join(lines, "\n"))
}

func (m Model) paletteView(width, height int) string {
	return m.renderPicker(pickerModel{
		title:        "Command Palette",
		query:        m.palette.query,
		focused:      m.palette.focus,
		items:        toPickerItems(m.filteredPaletteCommands()),
		emptyMessage: "No matching commands",
	}, width, height)
}

func toPickerItems(values []string) []pickerItem {
	items := make([]pickerItem, 0, len(values))
	for _, value := range values {
		items = append(items, pickerItem{key: value, label: value})
	}
	return items
}
