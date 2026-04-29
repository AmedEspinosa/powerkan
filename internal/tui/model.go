package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/amedespinosa/powerkan/internal/config"
	"github.com/amedespinosa/powerkan/internal/kanban"
	"github.com/amedespinosa/powerkan/internal/platform"
)

type route int

const (
	routeBoard route = iota
	routeSprints
	routeTickets
	routeExport
	routeTicketDetail
)

var navRoutes = []route{routeBoard, routeSprints, routeTickets, routeExport}

type inputMode int

const (
	modeNormal inputMode = iota
	modeInsert
)

type service interface {
	LoadBoard(ctx context.Context) (kanban.BoardData, error)
	ListTickets(ctx context.Context, filters kanban.TicketListFilters) (kanban.TicketListResult, error)
	GetTicketDetail(ctx context.Context, ticketID string) (kanban.TicketDetail, error)
	UpdateTicket(ctx context.Context, ticketID string, input kanban.UpdateTicketInput) (kanban.TicketDetail, error)
	MoveTicket(ctx context.Context, ticketID string, delta int) (kanban.TicketDetail, error)
	AddComment(ctx context.Context, ticketID string, input kanban.AddCommentInput) (kanban.TicketComment, error)
	ListEpics(ctx context.Context) ([]kanban.Epic, error)
	ListSprints(ctx context.Context, filters kanban.SprintListFilters) ([]kanban.SprintSummary, error)
	ExportTicketMarkdown(ctx context.Context, ticketID string, outPath string) (string, error)
	ExportTicketCSV(ctx context.Context, ticketID string, outPath string) (string, error)
}

type Dependencies struct {
	Config  config.Config
	Paths   platform.Paths
	Service service
}

type boardFilter struct {
	blockedOnly bool
}

type boardModel struct {
	data          kanban.BoardData
	filtered      []kanban.BoardColumn
	focusedColumn int
	focusedRows   []int
	searchQuery   string
	filter        boardFilter
}

type ticketsModel struct {
	data          kanban.TicketListResult
	focusedRow    int
	focusedColumn int
	editingCell   bool
	editingValue  string
	originalValue string
}

type detailModel struct {
	ticket        kanban.TicketDetail
	focusedField  int
	editingField  bool
	editingValue  string
	originalValue string
}

type exportModel struct {
	format string
}

// Model is the root Bubble Tea application state.
type Model struct {
	config         config.Config
	paths          platform.Paths
	service        service
	width          int
	height         int
	activeRoute    route
	previousRoute  route
	mode           inputMode
	board          boardModel
	tickets        ticketsModel
	detail         detailModel
	export         exportModel
	selectedTicket *kanban.TicketDetail
	statusMessage  string
	errorMessage   string
}

func NewModel(deps Dependencies) Model {
	m := Model{
		config:        deps.Config,
		paths:         deps.Paths,
		service:       deps.Service,
		activeRoute:   routeBoard,
		previousRoute: routeBoard,
		mode:          modeNormal,
		export:        exportModel{format: "md"},
	}
	m.refreshSupportData()
	m.refreshBoard("")
	m.refreshTickets()
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.mode == modeNormal && (msg.String() == "ctrl+c" || msg.String() == "q") {
			return m, tea.Quit
		}
		return m.handleKey(msg), nil
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading powerkan..."
	}

	header := renderHeader(m.width, m.activeRoute, m.board.searchQuery, m.board.filter)
	bodyHeight := max(m.height-4, 12)
	body := m.renderBody(bodyHeight)
	footer := renderFooter(m.width, m.activeRoute, m.mode, m.statusMessage, m.errorMessage)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func (m Model) handleKey(msg tea.KeyMsg) Model {
	if m.mode == modeInsert {
		return m.handleInsertKey(msg)
	}

	switch msg.String() {
	case "1":
		m.activeRoute = routeBoard
		m.mode = modeNormal
		return m
	case "2":
		m.activeRoute = routeSprints
		m.mode = modeNormal
		return m
	case "3":
		m.activeRoute = routeTickets
		m.mode = modeNormal
		return m
	case "4":
		m.activeRoute = routeExport
		m.mode = modeNormal
		return m
	}

	switch m.activeRoute {
	case routeBoard:
		return m.handleBoardNormalKey(msg)
	case routeTickets:
		return m.handleTicketsNormalKey(msg)
	case routeTicketDetail:
		return m.handleDetailNormalKey(msg)
	case routeExport:
		return m.handleExportNormalKey(msg)
	default:
		return m
	}
}

func (m Model) handleInsertKey(msg tea.KeyMsg) Model {
	switch msg.String() {
	case "esc":
		m.mode = modeNormal
		m.cancelEdit()
		return m
	case "enter":
		m.mode = modeNormal
		return m.commitEdit()
	case "backspace":
		m.updateActiveBuffer(trimLastRune(m.activeBuffer()))
		return m
	case "space":
		m.updateActiveBuffer(m.activeBuffer() + " ")
		return m
	}

	if msg.Type == tea.KeyRunes {
		m.updateActiveBuffer(m.activeBuffer() + string(msg.Runes))
	}
	return m
}

func (m Model) activeBuffer() string {
	switch {
	case m.activeRoute == routeBoard:
		return m.board.searchQuery
	case m.activeRoute == routeTickets && m.tickets.editingCell:
		return m.tickets.editingValue
	case m.activeRoute == routeTicketDetail && m.detail.editingField:
		return m.detail.editingValue
	default:
		return ""
	}
}

func (m *Model) updateActiveBuffer(value string) {
	switch {
	case m.activeRoute == routeBoard:
		m.board.searchQuery = value
		m.applyBoardFilters()
	case m.activeRoute == routeTickets && m.tickets.editingCell:
		m.tickets.editingValue = value
	case m.activeRoute == routeTicketDetail && m.detail.editingField:
		m.detail.editingValue = value
	}
}

func (m *Model) cancelEdit() {
	switch {
	case m.activeRoute == routeTickets && m.tickets.editingCell:
		m.tickets.editingValue = m.tickets.originalValue
		m.tickets.editingCell = false
	case m.activeRoute == routeTicketDetail && m.detail.editingField:
		m.detail.editingValue = m.detail.originalValue
		m.detail.editingField = false
	}
}

func (m Model) commitEdit() Model {
	switch {
	case m.activeRoute == routeBoard:
		m.applyBoardFilters()
		return m
	case m.activeRoute == routeTickets && m.tickets.editingCell:
		return m.commitTableEdit()
	case m.activeRoute == routeTicketDetail && m.detail.editingField:
		return m.commitDetailEdit()
	default:
		return m
	}
}

func (m *Model) refreshSupportData() {
	if m.service == nil {
		return
	}
	summaries, err := m.service.ListSprints(context.Background(), kanban.SprintListFilters{})
	if err == nil {
		sprints := make([]kanban.Sprint, 0, len(summaries))
		for _, summary := range summaries {
			sprints = append(sprints, summary.Sprint)
		}
		m.tickets.data.Sprints = sprints
	}
	epics, err := m.service.ListEpics(context.Background())
	if err == nil {
		m.tickets.data.Epics = epics
	}
}

func (m *Model) refreshBoard(preferTicketID string) {
	if m.service == nil {
		return
	}
	boardData, err := m.service.LoadBoard(context.Background())
	if err != nil {
		m.errorMessage = err.Error()
		return
	}
	m.errorMessage = ""
	m.board.data = boardData
	if len(m.board.focusedRows) != len(boardData.Columns) {
		m.board.focusedRows = make([]int, len(boardData.Columns))
	}
	m.applyBoardFilters()
	m.restoreBoardSelection(preferTicketID)
}

func (m *Model) refreshTickets() {
	if m.service == nil {
		return
	}
	result, err := m.service.ListTickets(context.Background(), kanban.TicketListFilters{})
	if err != nil {
		m.errorMessage = err.Error()
		return
	}
	m.errorMessage = ""
	m.tickets.data = result
	m.refreshSupportData()
	if m.tickets.focusedRow >= len(result.Tickets) {
		m.tickets.focusedRow = max(0, len(result.Tickets)-1)
	}
}

func (m *Model) loadSelectedTicket(ticketID string) {
	if m.service == nil || ticketID == "" {
		m.selectedTicket = nil
		return
	}
	detail, err := m.service.GetTicketDetail(context.Background(), ticketID)
	if err != nil {
		m.errorMessage = err.Error()
		m.selectedTicket = nil
		return
	}
	m.errorMessage = ""
	m.selectedTicket = &detail
}

func (m *Model) restoreBoardSelection(preferTicketID string) {
	if len(m.board.filtered) == 0 {
		m.selectedTicket = nil
		return
	}
	if preferTicketID != "" {
		for colIdx, column := range m.board.filtered {
			for rowIdx, ticket := range column.Tickets {
				if ticket.TicketID == preferTicketID {
					m.board.focusedColumn = colIdx
					m.board.focusedRows[colIdx] = rowIdx
					m.loadSelectedTicket(preferTicketID)
					return
				}
			}
		}
	}

	if m.board.focusedColumn >= len(m.board.filtered) {
		m.board.focusedColumn = len(m.board.filtered) - 1
	}
	if m.board.focusedColumn < 0 {
		m.board.focusedColumn = 0
	}

	column := m.board.filtered[m.board.focusedColumn]
	if len(column.Tickets) == 0 {
		m.selectedTicket = nil
		return
	}
	if m.board.focusedRows[m.board.focusedColumn] >= len(column.Tickets) {
		m.board.focusedRows[m.board.focusedColumn] = len(column.Tickets) - 1
	}
	if m.board.focusedRows[m.board.focusedColumn] < 0 {
		m.board.focusedRows[m.board.focusedColumn] = 0
	}
	m.loadSelectedTicket(column.Tickets[m.board.focusedRows[m.board.focusedColumn]].TicketID)
}

func (m *Model) applyBoardFilters() {
	filtered := make([]kanban.BoardColumn, 0, len(m.board.data.Columns))
	query := strings.ToLower(strings.TrimSpace(m.board.searchQuery))
	for _, column := range m.board.data.Columns {
		next := kanban.BoardColumn{Status: column.Status}
		for _, ticket := range column.Tickets {
			if m.board.filter.blockedOnly && !ticket.Blocked {
				continue
			}
			if query != "" {
				haystack := strings.ToLower(ticket.Title + "\n" + ticket.Description)
				if !strings.Contains(haystack, query) {
					continue
				}
			}
			next.Tickets = append(next.Tickets, ticket)
		}
		filtered = append(filtered, next)
	}
	m.board.filtered = filtered
}

func (m Model) openDetail(ticketID string) Model {
	if ticketID == "" {
		return m
	}
	if m.activeRoute != routeTicketDetail {
		m.previousRoute = m.activeRoute
	}
	m.activeRoute = routeTicketDetail
	m.mode = modeNormal
	m.loadSelectedTicket(ticketID)
	if m.selectedTicket != nil {
		m.detail.ticket = *m.selectedTicket
		m.detail.focusedField = 0
		m.detail.editingField = false
	}
	return m
}

func (m Model) selectedBoardTicket() *kanban.Ticket {
	if len(m.board.filtered) == 0 || m.board.focusedColumn >= len(m.board.filtered) {
		return nil
	}
	column := m.board.filtered[m.board.focusedColumn]
	if len(column.Tickets) == 0 {
		return nil
	}
	row := m.board.focusedRows[m.board.focusedColumn]
	if row < 0 || row >= len(column.Tickets) {
		return nil
	}
	ticket := column.Tickets[row]
	return &ticket
}

func (m Model) selectedTableTicket() *kanban.Ticket {
	if m.tickets.focusedRow < 0 || m.tickets.focusedRow >= len(m.tickets.data.Tickets) {
		return nil
	}
	ticket := m.tickets.data.Tickets[m.tickets.focusedRow]
	return &ticket
}

func routeTitle(r route) string {
	switch r {
	case routeBoard:
		return "Board"
	case routeSprints:
		return "Sprints"
	case routeTickets:
		return "Tickets"
	case routeExport:
		return "Export"
	case routeTicketDetail:
		return "Ticket Detail"
	default:
		return "Unknown"
	}
}

func renderHeader(width int, active route, search string, filter boardFilter) string {
	tabStyle := lipgloss.NewStyle().
		Padding(0, 1).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("250")).
		Foreground(lipgloss.Color("255"))
	activeTabStyle := tabStyle.Copy().BorderForeground(lipgloss.Color("208")).Foreground(lipgloss.Color("208")).Bold(true)
	tabs := make([]string, 0, len(navRoutes))
	for idx, r := range navRoutes {
		label := fmt.Sprintf("%d %s", idx+1, routeTitle(r))
		style := tabStyle
		if r == active {
			style = activeTabStyle
		}
		tabs = append(tabs, style.Render(label))
	}

	filterLabel := "All"
	if filter.blockedOnly {
		filterLabel = "Blocked"
	}
	right := lipgloss.JoinVertical(
		lipgloss.Right,
		tabStyle.Render("s Search: "+search),
		tabStyle.Render("f Filter: "+filterLabel),
	)
	leftWidth := max(0, width-lipgloss.Width(right)-2)
	left := lipgloss.NewStyle().Width(leftWidth).Render(strings.Join(tabs, " "))
	return lipgloss.NewStyle().Width(width).Padding(1, 1).Render(lipgloss.JoinHorizontal(lipgloss.Top, left, right))
}

func renderFooter(width int, active route, mode inputMode, status, errText string) string {
	modeText := "NORMAL"
	if mode == modeInsert {
		modeText = "INSERT"
	}
	help := "1-4 routes  q quit"
	switch active {
	case routeBoard:
		help = "h/l columns  j/k tickets  H/L move ticket  s search  f blocked filter  Enter detail"
	case routeTickets:
		help = "j/k rows  h/l columns  i edit  Enter detail"
	case routeTicketDetail:
		help = "j/k fields  i edit  Enter save  Esc back/cancel"
	case routeExport:
		help = "h/l format  Enter export"
	}
	if errText != "" {
		help = "error: " + errText
	} else if status != "" {
		help = status
	}

	style := lipgloss.NewStyle().
		Width(width).
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		Padding(0, 1).
		Foreground(lipgloss.Color("245"))
	return style.Render(modeText + " | " + help)
}

func renderPlaceholderPanel(width, height int, title, body string) string {
	panel := lipgloss.NewStyle().Width(width).Height(height).Padding(1, 2)
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	bodyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252")).MarginTop(1)
	return panel.Render(lipgloss.JoinVertical(lipgloss.Left, titleStyle.Render(title), bodyStyle.Render(body)))
}

func (m Model) renderBody(height int) string {
	switch m.activeRoute {
	case routeBoard:
		return m.boardView(m.width, height)
	case routeTickets:
		return m.ticketsView(m.width, height)
	case routeSprints:
		return renderPlaceholderPanel(m.width, height, "Sprints", "MVP placeholder. Active sprint data is surfaced on the board.")
	case routeExport:
		return m.exportView(m.width, height)
	case routeTicketDetail:
		return m.detailView(m.width, height)
	default:
		return renderPlaceholderPanel(m.width, height, "Unknown Route", "")
	}
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func parseBool(value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "blocked":
		return true, nil
	case "0", "false", "no", "n", "clear", "":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q", value)
	}
}

func parseStoryPoints(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("invalid story points %q", value)
	}
	return n, nil
}
