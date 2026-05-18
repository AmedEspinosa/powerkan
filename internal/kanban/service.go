package kanban

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/amedespinosa/powerkan/internal/config"
)

var (
	ErrNotFound             = errors.New("not found")
	ErrSprintOverlap        = errors.New("sprint overlaps an existing sprint")
	ErrEpicInUse            = errors.New("epic is referenced by existing tickets")
	ErrSprintInUse          = errors.New("sprint is referenced by existing tickets")
	ErrWebhookNotConfigured = errors.New("webhook endpoint_url is not configured")
	ErrActiveSprintExists   = errors.New("an active sprint already exists")
)

const DefaultEpicName = "Inbox"

type Service struct {
	db     *sql.DB
	config config.Config
	now    func() time.Time
	client *http.Client
}

func NewService(db *sql.DB, cfg config.Config) *Service {
	return &Service{
		db:     db,
		config: cfg,
		now:    time.Now,
		client: &http.Client{Timeout: time.Duration(cfg.Webhook.TimeoutSeconds) * time.Second},
	}
}

func (s *Service) SetNowFunc(now func() time.Time) {
	s.now = now
}

func (s *Service) SetHTTPClient(client *http.Client) {
	s.client = client
}

func (s *Service) LoadBoard(ctx context.Context) (BoardData, error) {
	scope := BoardScopeBacklog
	if sprint, err := s.GetActiveSprint(ctx); err == nil && sprint != nil {
		scope = BoardScopeSprint
	}
	return s.LoadBoardWithScope(ctx, scope)
}

func (s *Service) LoadBoardWithScope(ctx context.Context, scope BoardScope) (BoardData, error) {
	board := BoardData{Columns: make([]BoardColumn, 0, len(TicketStatuses))}
	for _, status := range TicketStatuses {
		board.Columns = append(board.Columns, BoardColumn{Status: status})
	}

	activeSprint, err := s.GetActiveSprint(ctx)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return BoardData{}, err
	}

	filters := TicketListFilters{}
	if scope == BoardScopeSprint && activeSprint != nil {
		filters.SprintID = &activeSprint.ID
	} else {
		scope = BoardScopeBacklog
		filters.Backlog = true
	}

	tickets, err := s.ListTickets(ctx, filters)
	if err != nil {
		return BoardData{}, err
	}
	totalPoints := 0.0
	donePoints := 0.0
	for _, ticket := range tickets.Tickets {
		totalPoints += ticket.StoryPoints
		if ticket.Status == TicketStatusDone {
			donePoints += ticket.StoryPoints
		}
		for i := range board.Columns {
			if board.Columns[i].Status == ticket.Status {
				board.Columns[i].Tickets = append(board.Columns[i].Tickets, ticket)
				break
			}
		}
	}

	metrics := BoardMetrics{
		Sprint:           activeSprint,
		PercentCompleted: ratio(donePoints, totalPoints),
		TotalPoints:      totalPoints,
		DonePoints:       donePoints,
		Scope:            scope,
	}
	if activeSprint != nil {
		location, err := s.location()
		if err != nil {
			return BoardData{}, err
		}
		endDate := dateInLocation(activeSprint.EndsOn, location)
		durationDays := inclusiveDays(dateInLocation(activeSprint.StartsOn, location), endDate)
		metrics.DaysLeft = max(0, inclusiveDays(truncateDate(s.now().In(location)), endDate))
		metrics.PointsPerDay = divide(totalPoints, durationDays)
	}
	board.Metrics = metrics
	return board, nil
}

func (s *Service) ListTickets(ctx context.Context, filters TicketListFilters) (TicketListResult, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 4)
	if filters.Backlog {
		where = append(where, "cur.sprint_id IS NULL")
	}
	if filters.SprintID != nil {
		where = append(where, "cur.sprint_id = ?")
		args = append(args, *filters.SprintID)
	}
	if filters.EpicID != nil {
		where = append(where, "t.epic_id = ?")
		args = append(args, *filters.EpicID)
	}
	if filters.Status != nil {
		where = append(where, "t.status = ?")
		args = append(args, string(*filters.Status))
	}

	query := ticketSelectQuery()
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += ticketOrderClause()

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return TicketListResult{}, err
	}
	defer rows.Close()

	var tickets []Ticket
	totalPoints := 0.0
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return TicketListResult{}, err
		}
		history, err := s.listSprintHistory(ctx, ticket.ID)
		if err != nil {
			return TicketListResult{}, err
		}
		ticket.SprintHistory = history
		tickets = append(tickets, ticket)
		totalPoints += ticket.StoryPoints
	}
	if err := rows.Err(); err != nil {
		return TicketListResult{}, err
	}

	epics, err := s.ListEpics(ctx)
	if err != nil {
		return TicketListResult{}, err
	}
	sprints, err := s.ListSprints(ctx, SprintListFilters{})
	if err != nil {
		return TicketListResult{}, err
	}

	out := make([]Sprint, 0, len(sprints))
	for _, sprint := range sprints {
		out = append(out, sprint.Sprint)
	}

	return TicketListResult{
		Tickets:      tickets,
		TotalPoints:  totalPoints,
		Epics:        epics,
		Sprints:      out,
		ActiveFilter: filters,
	}, nil
}

func (s *Service) GetTicketDetail(ctx context.Context, ticketID string) (TicketDetail, error) {
	row := s.db.QueryRowContext(ctx, ticketSelectQuery()+" WHERE t.ticket_id = ?", ticketID)
	ticket, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TicketDetail{}, ErrNotFound
		}
		return TicketDetail{}, err
	}
	ticket.SprintHistory, err = s.listSprintHistory(ctx, ticket.ID)
	if err != nil {
		return TicketDetail{}, err
	}
	comments, err := s.listCommentsByTicketInternalID(ctx, ticket.ID)
	if err != nil {
		return TicketDetail{}, err
	}
	return TicketDetail{Ticket: ticket, Comments: comments}, nil
}

func (s *Service) CreateEpic(ctx context.Context, input CreateEpicInput) (Epic, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = strings.TrimSpace(input.Name)
	}
	if title == "" {
		return Epic{}, fmt.Errorf("epic title is required")
	}
	status := input.Status
	if status == "" {
		status = EpicStatusOpen
	}
	if !isValidEpicStatus(status) {
		return Epic{}, fmt.Errorf("invalid epic status %q", status)
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO epics(title, status, color, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`,
		title, string(status), nullableColor(input.Color), now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return Epic{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Epic{}, err
	}
	return s.GetEpic(ctx, id)
}

func (s *Service) UpdateEpic(ctx context.Context, id int64, input UpdateEpicInput) (Epic, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = strings.TrimSpace(input.Name)
	}
	if title == "" {
		return Epic{}, fmt.Errorf("epic title is required")
	}
	if !isValidEpicStatus(input.Status) {
		return Epic{}, fmt.Errorf("invalid epic status %q", input.Status)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE epics SET title = ?, status = ?, color = ?, updated_at = ? WHERE id = ?`,
		title, string(input.Status), nullableColor(input.Color), s.now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return Epic{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Epic{}, err
	}
	if rowsAffected == 0 {
		return Epic{}, ErrNotFound
	}
	return s.GetEpic(ctx, id)
}

func (s *Service) GetEpic(ctx context.Context, id int64) (Epic, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, title, status, color, created_at, updated_at FROM epics WHERE id = ?`, id)
	epic, err := scanEpic(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Epic{}, ErrNotFound
		}
		return Epic{}, err
	}
	return epic, nil
}

func (s *Service) ListEpics(ctx context.Context) ([]Epic, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, title, status, color, created_at, updated_at FROM epics ORDER BY CASE status WHEN 'open' THEN 0 ELSE 1 END, title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var epics []Epic
	for rows.Next() {
		epic, err := scanEpic(rows)
		if err != nil {
			return nil, err
		}
		epics = append(epics, epic)
	}
	return epics, rows.Err()
}

func (s *Service) EnsureCreateEpic(ctx context.Context) (Epic, error) {
	epics, err := s.ListEpics(ctx)
	if err != nil {
		return Epic{}, err
	}
	if len(epics) > 0 {
		return epics[0], nil
	}
	return s.CreateEpic(ctx, CreateEpicInput{Title: DefaultEpicName, Status: EpicStatusOpen})
}

func (s *Service) DeleteEpic(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM epics WHERE id = ?`, id)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) AssignEpicToTicket(ctx context.Context, ticketID string, epicID int64) (TicketDetail, error) {
	detail, err := s.GetTicketDetail(ctx, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	input := ticketToUpdateInput(detail.Ticket)
	input.EpicID = &epicID
	return s.UpdateTicket(ctx, ticketID, input)
}

func (s *Service) ClearEpicForTicket(ctx context.Context, ticketID string) (TicketDetail, error) {
	detail, err := s.GetTicketDetail(ctx, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	input := ticketToUpdateInput(detail.Ticket)
	input.EpicID = nil
	return s.UpdateTicket(ctx, ticketID, input)
}

func (s *Service) CreateSprint(ctx context.Context, input CreateSprintInput) (Sprint, error) {
	if input.StartsOn.IsZero() {
		input.StartsOn = input.StartDate
	}
	if input.EndsOn.IsZero() {
		input.EndsOn = input.EndDate
	}
	if err := validateSprintDates(input.StartsOn, input.EndsOn); err != nil {
		return Sprint{}, err
	}
	if input.Status == "" {
		input.Status = SprintStatusPlanned
	}
	if !isValidSprintStatus(input.Status) {
		return Sprint{}, fmt.Errorf("invalid sprint status %q", input.Status)
	}
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sprint{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if input.Status == SprintStatusActive {
		if err := s.ensureNoOtherActiveSprint(ctx, tx, 0); err != nil {
			return Sprint{}, err
		}
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO sprints(name, goal, status, starts_on, ends_on, created_at, updated_at, closed_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, NULL)`,
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Goal), string(input.Status),
		truncateDate(input.StartsOn).Format("2006-01-02"), truncateDate(input.EndsOn).Format("2006-01-02"),
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return Sprint{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Sprint{}, err
	}
	if err := tx.Commit(); err != nil {
		return Sprint{}, err
	}
	return s.GetSprint(ctx, id)
}

func (s *Service) UpdateSprint(ctx context.Context, id int64, input UpdateSprintInput) (Sprint, error) {
	if input.StartsOn.IsZero() {
		input.StartsOn = input.StartDate
	}
	if input.EndsOn.IsZero() {
		input.EndsOn = input.EndDate
	}
	if err := validateSprintDates(input.StartsOn, input.EndsOn); err != nil {
		return Sprint{}, err
	}
	if !isValidSprintStatus(input.Status) {
		return Sprint{}, fmt.Errorf("invalid sprint status %q", input.Status)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sprint{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if input.Status == SprintStatusActive {
		if err := s.ensureNoOtherActiveSprint(ctx, tx, id); err != nil {
			return Sprint{}, err
		}
	}

	var closedAt any
	if input.Status == SprintStatusClosed {
		closedAt = s.now().UTC().Format(time.RFC3339)
	}

	result, err := tx.ExecContext(ctx, `UPDATE sprints
		SET name = ?, goal = ?, status = ?, starts_on = ?, ends_on = ?, updated_at = ?, closed_at = COALESCE(?, closed_at)
		WHERE id = ?`,
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Goal), string(input.Status),
		truncateDate(input.StartsOn).Format("2006-01-02"), truncateDate(input.EndsOn).Format("2006-01-02"),
		s.now().UTC().Format(time.RFC3339), closedAt, id)
	if err != nil {
		return Sprint{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Sprint{}, err
	}
	if rowsAffected == 0 {
		return Sprint{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Sprint{}, err
	}
	return s.GetSprint(ctx, id)
}

func (s *Service) GetSprint(ctx context.Context, id int64) (Sprint, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, goal, status, starts_on, ends_on, created_at, updated_at, closed_at FROM sprints WHERE id = ?`, id)
	sprint, err := scanSprint(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Sprint{}, ErrNotFound
		}
		return Sprint{}, err
	}
	return sprint, nil
}

func (s *Service) ListSprints(ctx context.Context, filters SprintListFilters) ([]SprintSummary, error) {
	query := `SELECT id, name, goal, status, starts_on, ends_on, created_at, updated_at, closed_at FROM sprints`
	args := []any{}
	if filters.Status != nil {
		query += ` WHERE status = ?`
		args = append(args, string(*filters.Status))
	}
	query += ` ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'planned' THEN 1 ELSE 2 END, starts_on DESC, id DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var summaries []SprintSummary
	for rows.Next() {
		sprint, err := scanSprint(rows)
		if err != nil {
			return nil, err
		}
		var totalPoints, pointsCompleted float64
		var ticketCount int
		if err := s.db.QueryRowContext(ctx, `
SELECT COALESCE(SUM(t.story_points), 0),
	COALESCE(SUM(CASE WHEN t.status = 'DONE' THEN t.story_points ELSE 0 END), 0),
	COUNT(1)
FROM sprint_tickets st
INNER JOIN tickets t ON t.id = st.ticket_id
WHERE st.sprint_id = ?`, sprint.ID).Scan(&totalPoints, &pointsCompleted, &ticketCount); err != nil {
			return nil, err
		}
		summaries = append(summaries, SprintSummary{
			Sprint:           sprint,
			PercentCompleted: ratio(pointsCompleted, totalPoints),
			PointsCompleted:  pointsCompleted,
			TotalPoints:      totalPoints,
			TicketCount:      ticketCount,
		})
	}
	return summaries, rows.Err()
}

func (s *Service) GetActiveSprint(ctx context.Context) (*Sprint, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, goal, status, starts_on, ends_on, created_at, updated_at, closed_at FROM sprints WHERE status = 'active' LIMIT 1`)
	sprint, err := scanSprint(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &sprint, nil
}

func (s *Service) ActivateSprint(ctx context.Context, id int64) (Sprint, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sprint{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if err := s.ensureNoOtherActiveSprint(ctx, tx, id); err != nil {
		return Sprint{}, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE sprints SET status = 'active', updated_at = ? WHERE id = ? AND status != 'closed'`,
		s.now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return Sprint{}, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return Sprint{}, err
	}
	if rowsAffected == 0 {
		return Sprint{}, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return Sprint{}, err
	}
	return s.GetSprint(ctx, id)
}

func (s *Service) ListIncompleteSprintTickets(ctx context.Context, sprintID int64) ([]Ticket, error) {
	filters := TicketListFilters{SprintID: &sprintID}
	result, err := s.ListTickets(ctx, filters)
	if err != nil {
		return nil, err
	}
	out := make([]Ticket, 0, len(result.Tickets))
	for _, ticket := range result.Tickets {
		if ticket.Status != TicketStatusDone {
			out = append(out, ticket)
		}
	}
	return out, nil
}

func (s *Service) CloseSprint(ctx context.Context, id int64, input CloseSprintInput) (Sprint, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Sprint{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var current Sprint
	row := tx.QueryRowContext(ctx, `SELECT id, name, goal, status, starts_on, ends_on, created_at, updated_at, closed_at FROM sprints WHERE id = ?`, id)
	current, err = scanSprint(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Sprint{}, ErrNotFound
		}
		return Sprint{}, err
	}
	if current.Status != SprintStatusActive {
		return Sprint{}, fmt.Errorf("sprint %d is not active", id)
	}

	var nextSprintID int64
	switch {
	case input.NextSprintID != nil:
		nextSprintID = *input.NextSprintID
	case input.CreateNextSprint != nil:
		if input.CreateNextSprint.StartsOn.IsZero() {
			input.CreateNextSprint.StartsOn = input.CreateNextSprint.StartDate
		}
		if input.CreateNextSprint.EndsOn.IsZero() {
			input.CreateNextSprint.EndsOn = input.CreateNextSprint.EndDate
		}
		if err := validateSprintDates(input.CreateNextSprint.StartsOn, input.CreateNextSprint.EndsOn); err != nil {
			return Sprint{}, err
		}
		create := *input.CreateNextSprint
		create.Status = SprintStatusPlanned
		result, err := tx.ExecContext(ctx, `INSERT INTO sprints(name, goal, status, starts_on, ends_on, created_at, updated_at, closed_at)
			VALUES(?, ?, 'planned', ?, ?, ?, ?, NULL)`,
			strings.TrimSpace(create.Name), strings.TrimSpace(create.Goal),
			truncateDate(create.StartsOn).Format("2006-01-02"), truncateDate(create.EndsOn).Format("2006-01-02"),
			s.now().UTC().Format(time.RFC3339), s.now().UTC().Format(time.RFC3339))
		if err != nil {
			return Sprint{}, err
		}
		nextSprintID, err = result.LastInsertId()
		if err != nil {
			return Sprint{}, err
		}
	default:
		return Sprint{}, fmt.Errorf("close sprint requires a next sprint")
	}

	var otherActive int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sprints WHERE status = 'active' AND id NOT IN (?, ?)`, id, nextSprintID).Scan(&otherActive); err != nil {
		return Sprint{}, err
	}
	if otherActive > 0 {
		return Sprint{}, ErrActiveSprintExists
	}

	closedAt := s.now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `UPDATE sprints SET status = 'closed', closed_at = ?, updated_at = ? WHERE id = ?`, closedAt, closedAt, id); err != nil {
		return Sprint{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sprint_tickets SET removed_at = ? WHERE sprint_id = ? AND removed_at IS NULL`, closedAt, id); err != nil {
		return Sprint{}, err
	}
	for _, ticketID := range input.CarryOverTicketIDs {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO sprint_tickets (sprint_id, ticket_id, added_at, removed_at)
SELECT ?, id, ?, NULL FROM tickets WHERE ticket_id = ?`, nextSprintID, closedAt, ticketID); err != nil {
			return Sprint{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sprints SET status = 'active', updated_at = ? WHERE id = ?`, closedAt, nextSprintID); err != nil {
		return Sprint{}, err
	}
	if err := tx.Commit(); err != nil {
		return Sprint{}, err
	}
	return s.GetSprint(ctx, id)
}

func (s *Service) AddTicketToActiveSprint(ctx context.Context, ticketID string) (TicketDetail, error) {
	active, err := s.GetActiveSprint(ctx)
	if err != nil {
		return TicketDetail{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	internalID, err := s.lookupTicketInternalID(ctx, tx, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE sprint_tickets SET removed_at = ? WHERE ticket_id = ? AND removed_at IS NULL`, s.now().UTC().Format(time.RFC3339), internalID); err != nil {
		return TicketDetail{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO sprint_tickets(sprint_id, ticket_id, added_at, removed_at) VALUES(?, ?, ?, NULL)`,
		active.ID, internalID, s.now().UTC().Format(time.RFC3339)); err != nil {
		return TicketDetail{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET updated_at = ? WHERE id = ?`, s.now().UTC().Format(time.RFC3339), internalID); err != nil {
		return TicketDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketDetail{}, err
	}
	detail, err := s.GetTicketDetail(ctx, ticketID)
	if err == nil {
		s.postTicketWebhookBestEffort(ctx, "ticket.updated", detail)
	}
	return detail, err
}

func (s *Service) RemoveTicketFromActiveSprint(ctx context.Context, ticketID string) (TicketDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	internalID, err := s.lookupTicketInternalID(ctx, tx, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sprint_tickets SET removed_at = ? WHERE ticket_id = ? AND removed_at IS NULL`,
		s.now().UTC().Format(time.RFC3339), internalID); err != nil {
		return TicketDetail{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET updated_at = ? WHERE id = ?`, s.now().UTC().Format(time.RFC3339), internalID); err != nil {
		return TicketDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketDetail{}, err
	}
	detail, err := s.GetTicketDetail(ctx, ticketID)
	if err == nil {
		s.postTicketWebhookBestEffort(ctx, "ticket.updated", detail)
	}
	return detail, err
}

func (s *Service) CreateTicket(ctx context.Context, input CreateTicketInput) (TicketDetail, error) {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	epicTitle, err := s.validateCreateOrUpdateTicketInput(ctx, tx, input.Title, input.Status, input.Type, input.StoryPoints, input.EpicID, input.SprintID)
	if err != nil {
		return TicketDetail{}, err
	}
	ticketID, err := s.generateUniqueTicketID(ctx, tx, epicTitle, input.Type)
	if err != nil {
		return TicketDetail{}, err
	}
	position, err := s.nextPosition(ctx, tx, input.SprintID, input.Status)
	if err != nil {
		return TicketDetail{}, err
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO tickets(ticket_id, title, status, type, blocked, story_points, epic_id, github_pr_url, description, position, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ticketID, strings.TrimSpace(input.Title), string(input.Status), string(input.Type), boolToInt(input.Blocked), input.StoryPoints,
		nullableInt64(input.EpicID), nullableString(input.GitHubPRURL), nullableString(input.Description), position,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return TicketDetail{}, err
	}
	internalID, err := result.LastInsertId()
	if err != nil {
		return TicketDetail{}, err
	}
	if input.SprintID != nil {
		if _, err := tx.ExecContext(ctx, `INSERT INTO sprint_tickets(sprint_id, ticket_id, added_at, removed_at) VALUES(?, ?, ?, NULL)`,
			*input.SprintID, internalID, now.Format(time.RFC3339)); err != nil {
			return TicketDetail{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return TicketDetail{}, err
	}

	detail, err := s.GetTicketDetail(ctx, ticketID)
	if err == nil {
		s.postTicketWebhookBestEffort(ctx, "ticket.created", detail)
	}
	return detail, err
}

func (s *Service) UpdateTicket(ctx context.Context, ticketID string, input UpdateTicketInput) (TicketDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.getTicketByIDTx(ctx, tx, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	if _, err := s.validateCreateOrUpdateTicketInput(ctx, tx, input.Title, input.Status, input.Type, input.StoryPoints, input.EpicID, nil); err != nil {
		return TicketDetail{}, err
	}

	position := current.Position
	if current.Status != input.Status {
		position, err = s.nextPosition(ctx, tx, current.CurrentSprintID, input.Status)
		if err != nil {
			return TicketDetail{}, err
		}
	}

	_, err = tx.ExecContext(ctx, `UPDATE tickets
		SET title = ?, status = ?, type = ?, blocked = ?, story_points = ?, epic_id = ?, github_pr_url = ?, description = ?, position = ?, updated_at = ?
		WHERE ticket_id = ?`,
		strings.TrimSpace(input.Title), string(input.Status), string(input.Type), boolToInt(input.Blocked), input.StoryPoints, nullableInt64(input.EpicID),
		nullableString(input.GitHubPRURL), nullableString(input.Description), position, s.now().UTC().Format(time.RFC3339), ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	if input.SprintID != nil || current.CurrentSprintID != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE sprint_tickets SET removed_at = ? WHERE ticket_id = ? AND removed_at IS NULL`,
			s.now().UTC().Format(time.RFC3339), current.ID); err != nil {
			return TicketDetail{}, err
		}
		if input.SprintID != nil {
			if _, err := tx.ExecContext(ctx, `INSERT INTO sprint_tickets(sprint_id, ticket_id, added_at, removed_at) VALUES(?, ?, ?, NULL)`,
				*input.SprintID, current.ID, s.now().UTC().Format(time.RFC3339)); err != nil {
				return TicketDetail{}, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return TicketDetail{}, err
	}
	detail, err := s.GetTicketDetail(ctx, ticketID)
	if err == nil {
		s.postTicketWebhookBestEffort(ctx, "ticket.updated", detail)
	}
	return detail, err
}

func (s *Service) DeleteTicket(ctx context.Context, ticketID string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM tickets WHERE ticket_id = ?`, ticketID)
	if err != nil {
		return err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) AddComment(ctx context.Context, ticketID string, input AddCommentInput) (TicketComment, error) {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketComment{}, err
	}
	defer func() { _ = tx.Rollback() }()

	internalID, err := s.lookupTicketInternalID(ctx, tx, ticketID)
	if err != nil {
		return TicketComment{}, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO ticket_comments(ticket_id, kind, body, created_at) VALUES(?, ?, ?, ?)`,
		internalID, string(input.Kind), strings.TrimSpace(input.Body), now.Format(time.RFC3339))
	if err != nil {
		return TicketComment{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return TicketComment{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketComment{}, err
	}
	return TicketComment{ID: id, TicketRef: internalID, Kind: input.Kind, Body: strings.TrimSpace(input.Body), CreatedAt: now}, nil
}

func (s *Service) MoveTicket(ctx context.Context, ticketID string, delta int) (TicketDetail, error) {
	detail, err := s.GetTicketDetail(ctx, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	index := slices.Index(TicketStatuses, detail.Status)
	if index < 0 {
		return TicketDetail{}, fmt.Errorf("unknown ticket status %q", detail.Status)
	}
	next := index + delta
	if next < 0 || next >= len(TicketStatuses) {
		return detail, nil
	}
	input := ticketToUpdateInput(detail.Ticket)
	input.Status = TicketStatuses[next]
	return s.UpdateTicket(ctx, ticketID, input)
}

func (s *Service) ReorderTicket(ctx context.Context, ticketID string, delta int) (TicketDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := s.getTicketByIDTx(ctx, tx, ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	rows, err := tx.QueryContext(ctx, reorderTicketQuery(current.CurrentSprintID), reorderArgs(current.Status, current.CurrentSprintID)...)
	if err != nil {
		return TicketDetail{}, err
	}
	defer rows.Close()

	type rowItem struct {
		id       int64
		position int
	}
	var items []rowItem
	currentIndex := -1
	for rows.Next() {
		var item rowItem
		if err := rows.Scan(&item.id, &item.position); err != nil {
			return TicketDetail{}, err
		}
		if item.id == current.ID {
			currentIndex = len(items)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return TicketDetail{}, err
	}
	if currentIndex < 0 {
		return TicketDetail{}, ErrNotFound
	}
	targetIndex := currentIndex + delta
	if targetIndex < 0 || targetIndex >= len(items) {
		if err := tx.Commit(); err != nil {
			return TicketDetail{}, err
		}
		return s.GetTicketDetail(ctx, ticketID)
	}
	currentPos := items[currentIndex].position
	targetPos := items[targetIndex].position
	now := s.now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET position = ?, updated_at = ? WHERE id = ?`, targetPos, now, items[currentIndex].id); err != nil {
		return TicketDetail{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET position = ?, updated_at = ? WHERE id = ?`, currentPos, now, items[targetIndex].id); err != nil {
		return TicketDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketDetail{}, err
	}
	return s.GetTicketDetail(ctx, ticketID)
}

func (s *Service) ExportTicketMarkdown(ctx context.Context, ticketID string, outPath string) (string, error) {
	data, err := s.GetTicketDetail(ctx, ticketID)
	if err != nil {
		return "", err
	}
	if outPath == "" {
		outPath = filepath.Join(s.config.Exports.DefaultDir, ticketID+".md")
	}
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(data.Title)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("- Ticket ID: `%s`\n", data.TicketID))
	b.WriteString(fmt.Sprintf("- Status: `%s`\n", data.Status))
	b.WriteString(fmt.Sprintf("- Type: `%s`\n", data.Type))
	if data.EpicID != nil {
		b.WriteString(fmt.Sprintf("- Epic: `%s`\n", data.EpicTitle))
		b.WriteString(fmt.Sprintf("- Epic Status: `%s`\n", data.EpicStatus))
		if data.EpicColor != nil && *data.EpicColor != "" {
			b.WriteString(fmt.Sprintf("- Epic Color: `%s`\n", *data.EpicColor))
		}
	} else {
		b.WriteString("- Epic: `NONE`\n")
	}
	if data.CurrentSprintID != nil {
		b.WriteString(fmt.Sprintf("- Current Sprint: `%s`\n", data.CurrentSprintName))
	} else {
		b.WriteString("- Current Sprint: `BACKLOG`\n")
	}
	b.WriteString(fmt.Sprintf("- Story Points: `%s`\n", FormatStoryPoints(data.StoryPoints)))
	b.WriteString(fmt.Sprintf("- Blocked: `%t`\n", data.Blocked))
	if data.GitHubPRURL != "" {
		b.WriteString(fmt.Sprintf("- GitHub PR: %s\n", data.GitHubPRURL))
	}
	b.WriteString("\n## Description\n\n")
	if data.Description == "" {
		b.WriteString("_No description._\n")
	} else {
		b.WriteString(data.Description)
		b.WriteString("\n")
	}
	b.WriteString("\n## Sprint History\n\n")
	if len(data.SprintHistory) == 0 {
		b.WriteString("_No sprint history._\n")
	} else {
		for _, membership := range data.SprintHistory {
			removedAt := "active"
			if membership.RemovedAt != nil {
				removedAt = membership.RemovedAt.Format(time.RFC3339)
			}
			b.WriteString(fmt.Sprintf("- %s [%s] %s -> %s\n",
				membership.SprintName,
				membership.SprintStatus,
				membership.AddedAt.Format(time.RFC3339),
				removedAt,
			))
		}
	}
	b.WriteString("\n## Comments\n\n")
	if len(data.Comments) == 0 {
		b.WriteString("_No comments._\n")
	} else {
		for _, comment := range data.Comments {
			b.WriteString(fmt.Sprintf("- %s [%s] %s\n", comment.CreatedAt.Format(time.RFC3339), comment.Kind, comment.Body))
		}
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return outPath, nil
}

func (s *Service) ExportTicketCSV(ctx context.Context, ticketID string, outPath string) (string, error) {
	data, err := s.GetTicketDetail(ctx, ticketID)
	if err != nil {
		return "", err
	}
	if outPath == "" {
		outPath = filepath.Join(s.config.Exports.DefaultDir, ticketID+".csv")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", err
	}

	file, err := os.OpenFile(outPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	writer := csv.NewWriter(file)
	header := []string{"ticket_id", "title", "status", "type", "epic_id", "epic_title", "current_sprint_id", "current_sprint_name", "story_points", "blocked", "github_pr_url", "description", "comments", "sprint_history_json"}
	if err := writer.Write(header); err != nil {
		return "", err
	}

	commentLines := make([]string, 0, len(data.Comments))
	for _, comment := range data.Comments {
		commentLines = append(commentLines, fmt.Sprintf("%s|%s|%s", comment.CreatedAt.Format(time.RFC3339), comment.Kind, comment.Body))
	}
	historyJSON, err := json.Marshal(buildWebhookSprintHistory(data.SprintHistory))
	if err != nil {
		return "", err
	}
	record := []string{
		data.TicketID,
		data.Title,
		string(data.Status),
		string(data.Type),
		formatOptionalInt64(data.EpicID),
		data.EpicTitle,
		formatOptionalInt64(data.CurrentSprintID),
		data.CurrentSprintName,
		FormatStoryPoints(data.StoryPoints),
		fmt.Sprintf("%t", data.Blocked),
		data.GitHubPRURL,
		data.Description,
		strings.Join(commentLines, "\n"),
		string(historyJSON),
	}
	if err := writer.Write(record); err != nil {
		return "", err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", err
	}
	return outPath, nil
}

func (s *Service) PostSprintEndWebhooks(ctx context.Context, sprintID int64, force bool) ([]WebhookPostResult, error) {
	if strings.TrimSpace(s.config.Webhook.EndpointURL) == "" {
		return nil, ErrWebhookNotConfigured
	}
	targets, err := s.endedSprints(ctx, sprintID)
	if err != nil {
		return nil, err
	}
	results := make([]WebhookPostResult, 0, len(targets))
	for _, sprint := range targets {
		payload := SprintWebhookPayload{
			StartDate:        sprint.StartsOn.Format("2006-01-02"),
			EndDate:          sprint.EndsOn.Format("2006-01-02"),
			TotalPoints:      sprint.TotalPoints,
			PointsCompleted:  sprint.PointsCompleted,
			PercentCompleted: sprint.PercentCompleted,
		}
		payloadHash, err := hashPayload(payload)
		if err != nil {
			return results, err
		}
		if !force {
			var count int
			if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM webhook_posts WHERE sprint_id = ? AND endpoint_url = ? AND payload_hash = ?`,
				sprint.ID, s.config.Webhook.EndpointURL, payloadHash).Scan(&count); err != nil {
				return results, err
			}
			if count > 0 {
				results = append(results, WebhookPostResult{SprintID: sprint.ID, Payload: payload, Skipped: true})
				continue
			}
		}
		if err := s.postWebhook(ctx, payload); err != nil {
			return results, err
		}
		if _, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO webhook_posts(sprint_id, endpoint_url, payload_hash, posted_at) VALUES(?, ?, ?, ?)`,
			sprint.ID, s.config.Webhook.EndpointURL, payloadHash, s.now().UTC().Format(time.RFC3339)); err != nil {
			return results, err
		}
		results = append(results, WebhookPostResult{SprintID: sprint.ID, Payload: payload})
	}
	return results, nil
}

func (s *Service) postWebhook(ctx context.Context, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	retries := max(1, s.config.Webhook.MaxRetries)
	backoff := time.Duration(s.config.Webhook.RetryBackoffSeconds) * time.Second
	var lastErr error
	for attempt := 1; attempt <= retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.Webhook.EndpointURL, strings.NewReader(string(body)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.client.Do(req)
		if err == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			_ = resp.Body.Close()
			return nil
		}
		if resp != nil {
			lastErr = fmt.Errorf("webhook responded with status %d", resp.StatusCode)
			_ = resp.Body.Close()
		} else {
			lastErr = err
		}
		if attempt < retries {
			timer := time.NewTimer(backoff)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return lastErr
}

func (s *Service) postTicketWebhookBestEffort(ctx context.Context, event string, detail TicketDetail) {
	if strings.TrimSpace(s.config.Webhook.EndpointURL) == "" {
		return
	}
	if err := s.postWebhook(ctx, buildTicketWebhookPayload(event, detail)); err != nil {
		log.Printf("ticket webhook failed event=%s ticket=%s error=%v", event, detail.TicketID, err)
	}
}

func (s *Service) endedSprints(ctx context.Context, sprintID int64) ([]SprintSummary, error) {
	sprints, err := s.ListSprints(ctx, SprintListFilters{})
	if err != nil {
		return nil, err
	}
	var out []SprintSummary
	for _, sprint := range sprints {
		if sprintID > 0 && sprint.ID != sprintID {
			continue
		}
		if sprint.Status != SprintStatusClosed {
			continue
		}
		out = append(out, sprint)
	}
	if sprintID > 0 && len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s *Service) listSprintHistory(ctx context.Context, internalID int64) ([]SprintTicket, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT st.sprint_id, s.name, s.status, st.added_at, st.removed_at
FROM sprint_tickets st
INNER JOIN sprints s ON s.id = st.sprint_id
WHERE st.ticket_id = ?
ORDER BY st.added_at ASC, st.sprint_id ASC`, internalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var history []SprintTicket
	for rows.Next() {
		var item SprintTicket
		var addedAt string
		var removedAt sql.NullString
		if err := rows.Scan(&item.SprintID, &item.SprintName, &item.SprintStatus, &addedAt, &removedAt); err != nil {
			return nil, err
		}
		item.AddedAt, err = parseTimestamp(addedAt)
		if err != nil {
			return nil, err
		}
		if removedAt.Valid {
			parsed, err := parseTimestamp(removedAt.String)
			if err != nil {
				return nil, err
			}
			item.RemovedAt = &parsed
		}
		history = append(history, item)
	}
	return history, rows.Err()
}

func (s *Service) listCommentsByTicketInternalID(ctx context.Context, internalID int64) ([]TicketComment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, ticket_id, kind, body, created_at FROM ticket_comments WHERE ticket_id = ? ORDER BY created_at DESC, id DESC`, internalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var comments []TicketComment
	for rows.Next() {
		var comment TicketComment
		var createdAt string
		if err := rows.Scan(&comment.ID, &comment.TicketRef, &comment.Kind, &comment.Body, &createdAt); err != nil {
			return nil, err
		}
		comment.CreatedAt, err = parseTimestamp(createdAt)
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	return comments, rows.Err()
}

func (s *Service) location() (*time.Location, error) {
	if s.config.App.Timezone == "" || strings.EqualFold(s.config.App.Timezone, "local") {
		return time.Local, nil
	}
	return time.LoadLocation(s.config.App.Timezone)
}

func (s *Service) ensureNoOtherActiveSprint(ctx context.Context, tx *sql.Tx, allowedID int64) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sprints WHERE status = 'active' AND id != ?`, allowedID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrActiveSprintExists
	}
	return nil
}

func (s *Service) generateUniqueTicketID(ctx context.Context, tx *sql.Tx, epicTitle string, ticketType TicketType) (string, error) {
	base := initials(epicTitle)
	if base == "" {
		base = "TKT"
	}
	typeCode := map[TicketType]string{
		TicketTypeFeature: "FEA",
		TicketTypeBug:     "BUG",
		TicketTypeFix:     "FIX",
		TicketTypeDocs:    "DOC",
	}[ticketType]
	layout := "0601021504"
	if strings.EqualFold(strings.TrimSpace(s.config.TicketID.TimestampFormat), "YYMMDDHHmmss") {
		layout = "060102150405"
	}
	now := s.now()
	candidates := []string{
		now.Format(layout),
		now.Format("060102150405"),
		now.Add(time.Second).Format("060102150405"),
	}
	for _, stamp := range candidates {
		candidate := fmt.Sprintf("%s-%s-%s", base, typeCode, stamp)
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM tickets WHERE ticket_id = ?`, candidate).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not generate unique ticket id")
}

func (s *Service) validateCreateOrUpdateTicketInput(ctx context.Context, tx *sql.Tx, title string, status TicketStatus, ticketType TicketType, storyPoints float64, epicID *int64, sprintID *int64) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", fmt.Errorf("title is required")
	}
	if !isValidTicketStatus(status) {
		return "", fmt.Errorf("invalid ticket status %q", status)
	}
	if !isValidTicketType(ticketType) {
		return "", fmt.Errorf("invalid ticket type %q", ticketType)
	}
	if !isFiniteStoryPoints(storyPoints) || storyPoints < 0 {
		return "", fmt.Errorf("story points must be greater than or equal to 0")
	}
	epicTitle := ""
	if epicID != nil {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM epics WHERE id = ?`, *epicID).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return "", fmt.Errorf("unknown epic id %d", *epicID)
		}
		if err := tx.QueryRowContext(ctx, `SELECT title FROM epics WHERE id = ?`, *epicID).Scan(&epicTitle); err != nil {
			return "", err
		}
	}
	if sprintID != nil {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM sprints WHERE id = ?`, *sprintID).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return "", fmt.Errorf("unknown sprint id %d", *sprintID)
		}
	}
	return epicTitle, nil
}

func (s *Service) nextPosition(ctx context.Context, tx *sql.Tx, sprintID *int64, status TicketStatus) (int, error) {
	var (
		query string
		args  []any
	)
	if sprintID != nil {
		query = `
SELECT COALESCE(MAX(t.position), -1) + 1
FROM tickets t
INNER JOIN sprint_tickets st ON st.ticket_id = t.id AND st.removed_at IS NULL AND st.sprint_id = ?
INNER JOIN sprints sp ON sp.id = st.sprint_id AND sp.status = 'active'
WHERE t.status = ?`
		args = []any{*sprintID, string(status)}
	} else {
		query = `
SELECT COALESCE(MAX(t.position), -1) + 1
FROM tickets t
LEFT JOIN sprint_tickets st ON st.ticket_id = t.id AND st.removed_at IS NULL
LEFT JOIN sprints sp ON sp.id = st.sprint_id AND sp.status = 'active'
WHERE t.status = ? AND sp.id IS NULL`
		args = []any{string(status)}
	}
	var next int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&next); err != nil {
		return 0, err
	}
	return next, nil
}

func (s *Service) lookupTicketInternalID(ctx context.Context, tx *sql.Tx, ticketID string) (int64, error) {
	var internalID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tickets WHERE ticket_id = ?`, ticketID).Scan(&internalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return internalID, nil
}

func (s *Service) getTicketByIDTx(ctx context.Context, tx *sql.Tx, ticketID string) (Ticket, error) {
	row := tx.QueryRowContext(ctx, ticketSelectQuery()+" WHERE t.ticket_id = ?", ticketID)
	ticket, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Ticket{}, ErrNotFound
		}
		return Ticket{}, err
	}
	return ticket, nil
}

func ticketSelectQuery() string {
	return `SELECT
	t.id,
	t.ticket_id,
	t.title,
	t.status,
	t.type,
	t.blocked,
	t.story_points,
	t.epic_id,
	COALESCE(e.title, ''),
	COALESCE(e.status, ''),
	e.color,
	cur.sprint_id,
	COALESCE(cur.name, ''),
	COALESCE(t.github_pr_url, ''),
	COALESCE(t.description, ''),
	COALESCE(t.position, 0),
	t.created_at,
	t.updated_at
FROM tickets t
LEFT JOIN epics e ON e.id = t.epic_id
LEFT JOIN (
	SELECT st.ticket_id, st.sprint_id, s.name
	FROM sprint_tickets st
	INNER JOIN sprints s ON s.id = st.sprint_id
	WHERE st.removed_at IS NULL AND s.status = 'active'
) cur ON cur.ticket_id = t.id`
}

func ticketOrderClause() string {
	return " ORDER BY CASE t.status WHEN 'NOT_STARTED' THEN 1 WHEN 'IN_PROGRESS' THEN 2 WHEN 'UNDER_REVIEW' THEN 3 ELSE 4 END, COALESCE(t.position, 0), t.updated_at DESC, t.id DESC"
}

func reorderTicketQuery(sprintID *int64) string {
	base := `SELECT t.id, COALESCE(t.position, 0) FROM tickets t `
	if sprintID != nil {
		base += `INNER JOIN sprint_tickets st ON st.ticket_id = t.id AND st.removed_at IS NULL AND st.sprint_id = ? INNER JOIN sprints s ON s.id = st.sprint_id AND s.status = 'active' `
		base += `WHERE t.status = ? ORDER BY COALESCE(t.position, 0), id`
		return base
	}
	base += `LEFT JOIN sprint_tickets st ON st.ticket_id = t.id AND st.removed_at IS NULL LEFT JOIN sprints s ON s.id = st.sprint_id AND s.status = 'active' `
	base += `WHERE t.status = ? AND s.id IS NULL ORDER BY COALESCE(t.position, 0), id`
	return base
}

func reorderArgs(status TicketStatus, sprintID *int64) []any {
	if sprintID != nil {
		return []any{*sprintID, string(status)}
	}
	return []any{string(status)}
}

func scanTicket(scanner interface{ Scan(dest ...any) error }) (Ticket, error) {
	var ticket Ticket
	var blocked int
	var epicID sql.NullInt64
	var epicStatus sql.NullString
	var epicColor sql.NullString
	var sprintID sql.NullInt64
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(
		&ticket.ID,
		&ticket.TicketID,
		&ticket.Title,
		&ticket.Status,
		&ticket.Type,
		&blocked,
		&ticket.StoryPoints,
		&epicID,
		&ticket.EpicTitle,
		&epicStatus,
		&epicColor,
		&sprintID,
		&ticket.CurrentSprintName,
		&ticket.GitHubPRURL,
		&ticket.Description,
		&ticket.Position,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Ticket{}, err
	}
	ticket.Blocked = blocked == 1
	if epicID.Valid {
		value := epicID.Int64
		ticket.EpicID = &value
	}
	ticket.EpicName = ticket.EpicTitle
	if epicStatus.Valid {
		ticket.EpicStatus = EpicStatus(epicStatus.String)
	}
	if epicColor.Valid {
		value := epicColor.String
		ticket.EpicColor = &value
	}
	if sprintID.Valid {
		value := sprintID.Int64
		ticket.CurrentSprintID = &value
	}
	ticket.SprintID = ticket.CurrentSprintID
	ticket.SprintName = ticket.CurrentSprintName
	var err error
	ticket.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return Ticket{}, err
	}
	ticket.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return Ticket{}, err
	}
	return ticket, nil
}

func scanEpic(scanner interface{ Scan(dest ...any) error }) (Epic, error) {
	var epic Epic
	var color sql.NullString
	var createdAt string
	var updatedAt string
	if err := scanner.Scan(&epic.ID, &epic.Title, &epic.Status, &color, &createdAt, &updatedAt); err != nil {
		return Epic{}, err
	}
	if color.Valid {
		value := color.String
		epic.Color = &value
	}
	epic.Name = epic.Title
	var err error
	epic.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return Epic{}, err
	}
	epic.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return Epic{}, err
	}
	return epic, nil
}

func scanSprint(scanner interface{ Scan(dest ...any) error }) (Sprint, error) {
	var sprint Sprint
	var startsOn string
	var endsOn string
	var createdAt string
	var updatedAt string
	var closedAt sql.NullString
	if err := scanner.Scan(&sprint.ID, &sprint.Name, &sprint.Goal, &sprint.Status, &startsOn, &endsOn, &createdAt, &updatedAt, &closedAt); err != nil {
		return Sprint{}, err
	}
	var err error
	sprint.StartsOn, err = parseDate(startsOn)
	if err != nil {
		return Sprint{}, err
	}
	sprint.EndsOn, err = parseDate(endsOn)
	if err != nil {
		return Sprint{}, err
	}
	sprint.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return Sprint{}, err
	}
	sprint.UpdatedAt, err = parseTimestamp(updatedAt)
	if err != nil {
		return Sprint{}, err
	}
	if closedAt.Valid {
		parsed, err := parseTimestamp(closedAt.String)
		if err != nil {
			return Sprint{}, err
		}
		sprint.ClosedAt = &parsed
	}
	sprint.StartsOn = truncateDate(sprint.StartsOn)
	sprint.EndsOn = truncateDate(sprint.EndsOn)
	sprint.StartDate = sprint.StartsOn
	sprint.EndDate = sprint.EndsOn
	sprint.CompletedAt = sprint.ClosedAt
	return sprint, nil
}

func parseTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	return time.Parse("2006-01-02 15:04:05", value)
}

func parseDate(value string) (time.Time, error) {
	if parsed, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, nil
	}
	return parseTimestamp(value)
}

func isFiniteStoryPoints(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func isValidTicketStatus(status TicketStatus) bool {
	for _, candidate := range TicketStatuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func isValidTicketType(ticketType TicketType) bool {
	for _, candidate := range TicketTypes {
		if candidate == ticketType {
			return true
		}
	}
	return false
}

func isValidSprintStatus(status SprintStatus) bool {
	for _, candidate := range SprintStatuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func isValidEpicStatus(status EpicStatus) bool {
	for _, candidate := range EpicStatuses {
		if candidate == status {
			return true
		}
	}
	return false
}

func validateSprintDates(startDate, endDate time.Time) error {
	startDate = truncateDate(startDate)
	endDate = truncateDate(endDate)
	if endDate.Before(startDate) {
		return fmt.Errorf("sprint end date must be on or after start date")
	}
	return nil
}

func truncateDate(value time.Time) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, value.Location())
}

func dateInLocation(value time.Time, location *time.Location) time.Time {
	return time.Date(value.Year(), value.Month(), value.Day(), 0, 0, 0, 0, location)
}

func inclusiveDays(start, end time.Time) int {
	start = truncateDate(start)
	end = truncateDate(end)
	if end.Before(start) {
		return 0
	}
	return int(end.Sub(start).Hours()/24) + 1
}

func ratio(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b * 100
}

func divide(a float64, b int) float64 {
	if b == 0 {
		return 0
	}
	return a / float64(b)
}

func initials(name string) string {
	re := regexp.MustCompile(`[A-Za-z]+`)
	parts := re.FindAllString(name, -1)
	var b strings.Builder
	for _, part := range parts {
		b.WriteByte(byte(strings.ToUpper(part[:1])[0]))
	}
	return b.String()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableString(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func nullableColor(value *string) any {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func hashPayload(payload SprintWebhookPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func buildTicketWebhookPayload(event string, detail TicketDetail) TicketWebhookPayload {
	var epic *TicketWebhookEpic
	if detail.EpicID != nil {
		epic = &TicketWebhookEpic{
			ID:     *detail.EpicID,
			Title:  detail.EpicTitle,
			Status: detail.EpicStatus,
			Color:  detail.EpicColor,
		}
	}
	var currentSprint *TicketWebhookSprint
	if detail.CurrentSprintID != nil {
		status := SprintStatusActive
		if len(detail.SprintHistory) > 0 {
			for i := len(detail.SprintHistory) - 1; i >= 0; i-- {
				if detail.SprintHistory[i].RemovedAt == nil && detail.SprintHistory[i].SprintID == *detail.CurrentSprintID {
					status = detail.SprintHistory[i].SprintStatus
					break
				}
			}
		}
		currentSprint = &TicketWebhookSprint{
			ID:     *detail.CurrentSprintID,
			Name:   detail.CurrentSprintName,
			Status: status,
		}
	}
	return TicketWebhookPayload{
		Event: event,
		Ticket: TicketWebhookData{
			TicketID:        detail.TicketID,
			Title:           detail.Title,
			Status:          detail.Status,
			Type:            detail.Type,
			Blocked:         detail.Blocked,
			StoryPoints:     detail.StoryPoints,
			EpicID:          detail.EpicID,
			Epic:            epic,
			CurrentSprintID: detail.CurrentSprintID,
			CurrentSprint:   currentSprint,
			SprintHistory:   buildWebhookSprintHistory(detail.SprintHistory),
			GitHubPRURL:     detail.GitHubPRURL,
			Description:     detail.Description,
			CreatedAt:       detail.CreatedAt.Format(time.RFC3339),
			UpdatedAt:       detail.UpdatedAt.Format(time.RFC3339),
		},
	}
}

func buildWebhookSprintHistory(history []SprintTicket) []TicketWebhookSprintLink {
	out := make([]TicketWebhookSprintLink, 0, len(history))
	for _, membership := range history {
		var removedAt *string
		if membership.RemovedAt != nil {
			value := membership.RemovedAt.Format(time.RFC3339)
			removedAt = &value
		}
		out = append(out, TicketWebhookSprintLink{
			SprintID:   membership.SprintID,
			SprintName: membership.SprintName,
			Status:     membership.SprintStatus,
			AddedAt:    membership.AddedAt.Format(time.RFC3339),
			RemovedAt:  removedAt,
		})
	}
	return out
}

func formatOptionalInt64(value *int64) string {
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%d", *value)
}

func ticketToUpdateInput(ticket Ticket) UpdateTicketInput {
	return UpdateTicketInput{
		Title:       ticket.Title,
		Status:      ticket.Status,
		Type:        ticket.Type,
		Blocked:     ticket.Blocked,
		StoryPoints: ticket.StoryPoints,
		EpicID:      ticket.EpicID,
		SprintID:    ticket.CurrentSprintID,
		GitHubPRURL: ticket.GitHubPRURL,
		Description: ticket.Description,
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
