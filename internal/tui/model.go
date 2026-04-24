package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amedespinosa/powerkan/internal/config"
	"github.com/amedespinosa/powerkan/internal/platform"
)

type route int

const (
	routeBoard route = iota
	routeTickets
	routeSprints
	routeTicketDetail
)

var routes = []route{routeBoard, routeTickets, routeSprints, routeTicketDetail}

// Model is the root Bubble Tea application state.
type Model struct {
	config      config.Config
	paths       platform.Paths
	width       int
	height      int
	activeRoute route
	board       boardModel
}

// NewModel constructs the TUI root model.
func NewModel(cfg config.Config, paths platform.Paths) Model {
	return Model{
		config:      cfg,
		paths:       paths,
		activeRoute: routeBoard,
		board:       newBoardModel(),
	}
}

// Init satisfies tea.Model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update satisfies tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "1":
			m.activeRoute = routeBoard
			return m, nil
		case "2":
			m.activeRoute = routeTickets
			return m, nil
		case "3":
			m.activeRoute = routeSprints
			return m, nil
		case "4":
			m.activeRoute = routeTicketDetail
			return m, nil
		case "enter":
			if m.activeRoute == routeBoard {
				m.activeRoute = routeTicketDetail
				return m, nil
			}
		case "H":
			m.activeRoute = previousRoute(m.activeRoute)
			return m, nil
		case "L":
			m.activeRoute = nextRoute(m.activeRoute)
			return m, nil
		}

		if m.activeRoute == routeBoard {
			updatedBoard, cmd := m.board.Update(msg)
			m.board = updatedBoard
			return m, cmd
		}
	}

	return m, nil
}

// View satisfies tea.Model.
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading powerkan..."
	}

	header := renderHeader(m.width, m.activeRoute)
	bodyHeight := max(m.height-4, 10)
	body := m.renderBody(bodyHeight)
	footer := renderFooter(m.width, m.activeRoute)

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) renderBody(height int) string {
	switch m.activeRoute {
	case routeBoard:
		return m.board.View(m.width, height)
	case routeTickets:
		return renderPlaceholderPanel(m.width, height, "Ticket List", "Phase 0 placeholder for backlog and table workflows.")
	case routeSprints:
		return renderPlaceholderPanel(m.width, height, "Sprints", "Phase 0 placeholder for sprint list and filters.")
	case routeTicketDetail:
		return renderPlaceholderPanel(m.width, height, "Ticket Detail", "Phase 0 placeholder opened from the board or route switcher.")
	default:
		return renderPlaceholderPanel(m.width, height, "Unknown Route", "")
	}
}

func previousRoute(current route) route {
	for i, candidate := range routes {
		if candidate == current {
			return routes[(i+len(routes)-1)%len(routes)]
		}
	}
	return routeBoard
}

func nextRoute(current route) route {
	for i, candidate := range routes {
		if candidate == current {
			return routes[(i+1)%len(routes)]
		}
	}
	return routeBoard
}

func routeTitle(r route) string {
	switch r {
	case routeBoard:
		return "Board"
	case routeTickets:
		return "Tickets"
	case routeSprints:
		return "Sprints"
	case routeTicketDetail:
		return "Ticket Detail"
	default:
		return "Unknown"
	}
}

func renderHeader(width int, active route) string {
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	tabStyle := lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("250"))
	activeTabStyle := tabStyle.Copy().Bold(true).Foreground(lipgloss.Color("230")).Background(lipgloss.Color("62"))

	tabs := make([]string, 0, len(routes))
	for idx, r := range routes {
		label := fmt.Sprintf("%d %s", idx+1, routeTitle(r))
		if r == active {
			tabs = append(tabs, activeTabStyle.Render(label))
			continue
		}
		tabs = append(tabs, tabStyle.Render(label))
	}

	header := lipgloss.JoinHorizontal(lipgloss.Top, titleStyle.Render("powerkan"), "  ", strings.Join(tabs, " "))
	border := lipgloss.NewStyle().Width(width).BorderBottom(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("238"))
	return border.Render(header)
}

func renderFooter(width int, active route) string {
	help := "H/L switch screens  1-4 jump routes  q quit"
	if active == routeBoard {
		help = "h/l or left/right focus board  H/L switch screens  Enter open detail  q quit"
	}

	style := lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("245")).
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("238"))

	return style.Render(help)
}

func renderPlaceholderPanel(width, height int, title, body string) string {
	panel := lipgloss.NewStyle().
		Width(width).
		Height(height).
		Padding(1, 2)

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).MarginTop(1)

	return panel.Render(lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(title), bodyStyle.Render(body)))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
