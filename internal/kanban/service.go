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
)

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
	board := BoardData{Columns: make([]BoardColumn, 0, len(TicketStatuses))}
	for _, status := range TicketStatuses {
		board.Columns = append(board.Columns, BoardColumn{Status: status})
	}

	sprint, err := s.GetActiveSprint(ctx)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return board, nil
		}
		return BoardData{}, err
	}

	tickets, err := s.listTicketsForSprint(ctx, &sprint.ID)
	if err != nil {
		return BoardData{}, err
	}

	totalPoints := 0
	donePoints := 0
	for _, ticket := range tickets {
		totalPoints += ticket.StoryPoints
		if ticket.Status == TicketStatusDone {
			donePoints += ticket.StoryPoints
		}
		for i := range board.Columns {
			if board.Columns[i].Status == ticket.Status {
				board.Columns[i].Tickets = append(board.Columns[i].Tickets, ticket)
			}
		}
	}

	location, err := s.location()
	if err != nil {
		return BoardData{}, err
	}
	startDate := dateInLocation(sprint.StartDate, location)
	endDate := dateInLocation(sprint.EndDate, location)
	durationDays := inclusiveDays(startDate, endDate)
	metrics := BoardMetrics{
		Sprint:           sprint,
		DaysLeft:         max(0, inclusiveDays(truncateDate(s.now().In(location)), endDate)),
		PercentCompleted: ratio(donePoints, totalPoints),
		PointsPerDay:     divide(totalPoints, durationDays),
		TotalPoints:      totalPoints,
		DonePoints:       donePoints,
	}
	board.Metrics = metrics

	return board, nil
}

func (s *Service) ListTickets(ctx context.Context, filters TicketListFilters) (TicketListResult, error) {
	where := make([]string, 0, 4)
	args := make([]any, 0, 4)
	switch {
	case filters.Backlog:
		where = append(where, "t.sprint_id IS NULL")
	case filters.SprintID != nil:
		where = append(where, "t.sprint_id = ?")
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

	query := `SELECT t.id, t.ticket_id, t.title, t.status, t.type, t.blocked, t.story_points,
		t.epic_id, e.name, t.sprint_id, COALESCE(s.name, ''), COALESCE(t.github_pr_url, ''),
		COALESCE(t.description, ''), COALESCE(t.position, 0), t.created_at, t.updated_at
	FROM tickets t
	INNER JOIN epics e ON e.id = t.epic_id
	LEFT JOIN sprints s ON s.id = t.sprint_id`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY CASE t.status WHEN 'NOT_STARTED' THEN 1 WHEN 'IN_PROGRESS' THEN 2 WHEN 'UNDER_REVIEW' THEN 3 ELSE 4 END, COALESCE(t.position, 0), t.updated_at DESC, t.id DESC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return TicketListResult{}, err
	}
	defer rows.Close()

	var tickets []Ticket
	totalPoints := 0
	for rows.Next() {
		ticket, err := scanTicket(rows)
		if err != nil {
			return TicketListResult{}, err
		}
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
	row := s.db.QueryRowContext(ctx, `SELECT t.id, t.ticket_id, t.title, t.status, t.type, t.blocked, t.story_points,
		t.epic_id, e.name, t.sprint_id, COALESCE(sp.name, ''), COALESCE(t.github_pr_url, ''),
		COALESCE(t.description, ''), COALESCE(t.position, 0), t.created_at, t.updated_at
	FROM tickets t
	INNER JOIN epics e ON e.id = t.epic_id
	LEFT JOIN sprints sp ON sp.id = t.sprint_id
	WHERE t.ticket_id = ?`, ticketID)
	ticket, err := scanTicket(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TicketDetail{}, ErrNotFound
		}
		return TicketDetail{}, err
	}

	comments, err := s.listCommentsByTicketInternalID(ctx, ticket.ID)
	if err != nil {
		return TicketDetail{}, err
	}

	return TicketDetail{Ticket: ticket, Comments: comments}, nil
}

func (s *Service) CreateEpic(ctx context.Context, input CreateEpicInput) (Epic, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Epic{}, fmt.Errorf("epic name is required")
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO epics(name, created_at) VALUES(?, ?)`, name, now.Format(time.RFC3339))
	if err != nil {
		return Epic{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Epic{}, err
	}
	return Epic{ID: id, Name: name, CreatedAt: now}, nil
}

func (s *Service) ListEpics(ctx context.Context) ([]Epic, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, created_at FROM epics ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var epics []Epic
	for rows.Next() {
		var epic Epic
		var createdAt string
		if err := rows.Scan(&epic.ID, &epic.Name, &createdAt); err != nil {
			return nil, err
		}
		epic.CreatedAt, err = parseTimestamp(createdAt)
		if err != nil {
			return nil, err
		}
		epics = append(epics, epic)
	}
	return epics, rows.Err()
}

func (s *Service) DeleteEpic(ctx context.Context, id int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tickets WHERE epic_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrEpicInUse
	}
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

func (s *Service) CreateSprint(ctx context.Context, input CreateSprintInput) (Sprint, error) {
	if err := validateSprintDates(input.StartDate, input.EndDate); err != nil {
		return Sprint{}, err
	}
	if err := s.ensureSprintNoOverlap(ctx, 0, input.StartDate, input.EndDate); err != nil {
		return Sprint{}, err
	}
	now := s.now().UTC()
	result, err := s.db.ExecContext(ctx, `INSERT INTO sprints(name, quarter, start_date, end_date, created_at) VALUES(?, ?, ?, ?, ?)`,
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Quarter), input.StartDate.Format("2006-01-02"), input.EndDate.Format("2006-01-02"), now.Format(time.RFC3339))
	if err != nil {
		return Sprint{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Sprint{}, err
	}
	sprint := Sprint{
		ID:        id,
		Name:      strings.TrimSpace(input.Name),
		Quarter:   strings.TrimSpace(input.Quarter),
		StartDate: truncateDate(input.StartDate),
		EndDate:   truncateDate(input.EndDate),
		CreatedAt: now,
	}
	sprint.Status, _ = s.deriveSprintStatus(sprint)
	return sprint, nil
}

func (s *Service) UpdateSprint(ctx context.Context, id int64, input UpdateSprintInput) (Sprint, error) {
	if err := validateSprintDates(input.StartDate, input.EndDate); err != nil {
		return Sprint{}, err
	}
	if err := s.ensureSprintNoOverlap(ctx, id, input.StartDate, input.EndDate); err != nil {
		return Sprint{}, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE sprints SET name = ?, quarter = ?, start_date = ?, end_date = ? WHERE id = ?`,
		strings.TrimSpace(input.Name), strings.TrimSpace(input.Quarter), input.StartDate.Format("2006-01-02"), input.EndDate.Format("2006-01-02"), id)
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
	return s.GetSprint(ctx, id)
}

func (s *Service) DeleteSprint(ctx context.Context, id int64) error {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM tickets WHERE sprint_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrSprintInUse
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM sprints WHERE id = ?`, id)
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

func (s *Service) GetSprint(ctx context.Context, id int64) (Sprint, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, quarter, start_date, end_date, created_at, completed_at FROM sprints WHERE id = ?`, id)
	sprint, err := scanSprint(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Sprint{}, ErrNotFound
		}
		return Sprint{}, err
	}
	sprint.Status, _ = s.deriveSprintStatus(sprint)
	return sprint, nil
}

func (s *Service) ListSprints(ctx context.Context, filters SprintListFilters) ([]SprintSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, quarter, start_date, end_date, created_at, completed_at FROM sprints ORDER BY start_date DESC, id DESC`)
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
		status, err := s.deriveSprintStatus(sprint)
		if err != nil {
			return nil, err
		}
		sprint.Status = status
		if filters.Quarter != nil && sprint.Quarter != *filters.Quarter {
			continue
		}
		if filters.Status != nil && sprint.Status != *filters.Status {
			continue
		}

		var totalPoints, pointsCompleted, ticketCount int
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(story_points), 0), COALESCE(SUM(CASE WHEN status = 'DONE' THEN story_points ELSE 0 END), 0), COUNT(1)
			FROM tickets WHERE sprint_id = ?`, sprint.ID).Scan(&totalPoints, &pointsCompleted, &ticketCount); err != nil {
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
	location, err := s.location()
	if err != nil {
		return nil, err
	}
	today := truncateDate(s.now().In(location)).Format("2006-01-02")
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, quarter, start_date, end_date, created_at, completed_at
		FROM sprints WHERE start_date <= ? AND end_date >= ? ORDER BY start_date DESC, id DESC`, today, today)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var active *Sprint
	for rows.Next() {
		sprint, err := scanSprint(rows)
		if err != nil {
			return nil, err
		}
		status, err := s.deriveSprintStatus(sprint)
		if err != nil {
			return nil, err
		}
		sprint.Status = status
		if active == nil {
			copy := sprint
			active = &copy
			continue
		}
		return nil, fmt.Errorf("%w: %d and %d", ErrSprintOverlap, active.ID, sprint.ID)
	}
	if active == nil {
		return nil, ErrNotFound
	}
	return active, nil
}

func (s *Service) CreateTicket(ctx context.Context, input CreateTicketInput) (TicketDetail, error) {
	now := s.now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	epicName, err := lookupEpicName(ctx, tx, input.EpicID)
	if err != nil {
		return TicketDetail{}, err
	}
	ticketID, err := s.generateUniqueTicketID(ctx, tx, epicName, input.Type)
	if err != nil {
		return TicketDetail{}, err
	}
	position, err := nextPosition(ctx, tx, input.SprintID, input.Status)
	if err != nil {
		return TicketDetail{}, err
	}

	result, err := tx.ExecContext(ctx, `INSERT INTO tickets(ticket_id, title, status, type, blocked, story_points, epic_id, sprint_id, github_pr_url, description, position, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ticketID, strings.TrimSpace(input.Title), string(input.Status), string(input.Type), boolToInt(input.Blocked), input.StoryPoints,
		input.EpicID, nullableInt64(input.SprintID), nullableString(input.GitHubPRURL), nullableString(input.Description), position, now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return TicketDetail{}, err
	}
	if _, err := result.LastInsertId(); err != nil {
		return TicketDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketDetail{}, err
	}

	return s.GetTicketDetail(ctx, ticketID)
}

func (s *Service) UpdateTicket(ctx context.Context, ticketID string, input UpdateTicketInput) (TicketDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var current Ticket
	row := tx.QueryRowContext(ctx, `SELECT t.id, t.ticket_id, t.title, t.status, t.type, t.blocked, t.story_points,
		t.epic_id, e.name, t.sprint_id, COALESCE(sp.name, ''), COALESCE(t.github_pr_url, ''), COALESCE(t.description, ''),
		COALESCE(t.position, 0), t.created_at, t.updated_at
	FROM tickets t
	INNER JOIN epics e ON e.id = t.epic_id
	LEFT JOIN sprints sp ON sp.id = t.sprint_id
	WHERE t.ticket_id = ?`, ticketID)
	current, err = scanTicket(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TicketDetail{}, ErrNotFound
		}
		return TicketDetail{}, err
	}

	position := current.Position
	if current.Status != input.Status || !sameOptionalID(current.SprintID, input.SprintID) {
		position, err = nextPosition(ctx, tx, input.SprintID, input.Status)
		if err != nil {
			return TicketDetail{}, err
		}
	}

	_, err = tx.ExecContext(ctx, `UPDATE tickets
		SET title = ?, status = ?, type = ?, blocked = ?, story_points = ?, epic_id = ?, sprint_id = ?, github_pr_url = ?, description = ?, position = ?, updated_at = ?
		WHERE ticket_id = ?`,
		strings.TrimSpace(input.Title), string(input.Status), string(input.Type), boolToInt(input.Blocked), input.StoryPoints, input.EpicID,
		nullableInt64(input.SprintID), nullableString(input.GitHubPRURL), nullableString(input.Description), position, s.now().UTC().Format(time.RFC3339), ticketID)
	if err != nil {
		return TicketDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return TicketDetail{}, err
	}
	return s.GetTicketDetail(ctx, ticketID)
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

	var internalID int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tickets WHERE ticket_id = ?`, ticketID).Scan(&internalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TicketComment{}, ErrNotFound
		}
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
	return s.UpdateTicket(ctx, ticketID, UpdateTicketInput{
		Title:       detail.Title,
		Status:      TicketStatuses[next],
		Type:        detail.Type,
		Blocked:     detail.Blocked,
		StoryPoints: detail.StoryPoints,
		EpicID:      detail.EpicID,
		SprintID:    detail.SprintID,
		GitHubPRURL: detail.GitHubPRURL,
		Description: detail.Description,
	})
}

func (s *Service) ReorderTicket(ctx context.Context, ticketID string, delta int) (TicketDetail, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TicketDetail{}, err
	}
	defer func() { _ = tx.Rollback() }()

	currentRow := tx.QueryRowContext(ctx, `SELECT id, sprint_id, status, COALESCE(position, 0) FROM tickets WHERE ticket_id = ?`, ticketID)
	var internalID int64
	var sprintID sql.NullInt64
	var status string
	var position int
	if err := currentRow.Scan(&internalID, &sprintID, &status, &position); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TicketDetail{}, ErrNotFound
		}
		return TicketDetail{}, err
	}

	query := `SELECT id, COALESCE(position, 0) FROM tickets WHERE status = ? AND `
	args := []any{status}
	if sprintID.Valid {
		query += `sprint_id = ? `
		args = append(args, sprintID.Int64)
	} else {
		query += `sprint_id IS NULL `
	}
	query += `ORDER BY COALESCE(position, 0), id`
	rows, err := tx.QueryContext(ctx, query, args...)
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
		if item.id == internalID {
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
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET position = ?, updated_at = ? WHERE id = ?`, targetPos, s.now().UTC().Format(time.RFC3339), items[currentIndex].id); err != nil {
		return TicketDetail{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tickets SET position = ?, updated_at = ? WHERE id = ?`, currentPos, s.now().UTC().Format(time.RFC3339), items[targetIndex].id); err != nil {
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
	b.WriteString(fmt.Sprintf("- Epic: `%s`\n", data.EpicName))
	if data.SprintID != nil {
		b.WriteString(fmt.Sprintf("- Sprint: `%s`\n", data.SprintName))
	} else {
		b.WriteString("- Sprint: `BACKLOG`\n")
	}
	b.WriteString(fmt.Sprintf("- Story Points: `%d`\n", data.StoryPoints))
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
	header := []string{"ticket_id", "title", "status", "type", "epic", "sprint", "story_points", "blocked", "github_pr_url", "description", "comments"}
	if err := writer.Write(header); err != nil {
		return "", err
	}

	commentLines := make([]string, 0, len(data.Comments))
	for _, comment := range data.Comments {
		commentLines = append(commentLines, fmt.Sprintf("%s|%s|%s", comment.CreatedAt.Format(time.RFC3339), comment.Kind, comment.Body))
	}
	sprintName := "BACKLOG"
	if data.SprintID != nil {
		sprintName = data.SprintName
	}
	record := []string{
		data.TicketID,
		data.Title,
		string(data.Status),
		string(data.Type),
		data.EpicName,
		sprintName,
		fmt.Sprintf("%d", data.StoryPoints),
		fmt.Sprintf("%t", data.Blocked),
		data.GitHubPRURL,
		data.Description,
		strings.Join(commentLines, "\n"),
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
			StartDate:        sprint.StartDate.Format("2006-01-02"),
			EndDate:          sprint.EndDate.Format("2006-01-02"),
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

func (s *Service) postWebhook(ctx context.Context, payload SprintWebhookPayload) error {
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

func (s *Service) endedSprints(ctx context.Context, sprintID int64) ([]SprintSummary, error) {
	var filters SprintListFilters
	sprints, err := s.ListSprints(ctx, filters)
	if err != nil {
		return nil, err
	}
	location, err := s.location()
	if err != nil {
		return nil, err
	}
	today := truncateDate(s.now().In(location))
	var out []SprintSummary
	for _, sprint := range sprints {
		if sprintID > 0 && sprint.ID != sprintID {
			continue
		}
		if dateInLocation(sprint.EndDate, location).After(today) {
			continue
		}
		out = append(out, sprint)
	}
	if sprintID > 0 && len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

func (s *Service) listTicketsForSprint(ctx context.Context, sprintID *int64) ([]Ticket, error) {
	result, err := s.ListTickets(ctx, TicketListFilters{SprintID: sprintID})
	if err != nil {
		return nil, err
	}
	return result.Tickets, nil
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

func (s *Service) deriveSprintStatus(sprint Sprint) (SprintStatus, error) {
	location, err := s.location()
	if err != nil {
		return SprintStatusNotStarted, err
	}
	today := truncateDate(s.now().In(location))
	start := dateInLocation(sprint.StartDate, location)
	end := dateInLocation(sprint.EndDate, location)
	switch {
	case today.Before(start):
		return SprintStatusNotStarted, nil
	case today.After(end):
		return SprintStatusDone, nil
	default:
		return SprintStatusInProgress, nil
	}
}

func (s *Service) ensureSprintNoOverlap(ctx context.Context, sprintID int64, startDate, endDate time.Time) error {
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sprints WHERE id != ? AND start_date <= ? AND end_date >= ?`,
		sprintID, endDate.Format("2006-01-02"), startDate.Format("2006-01-02"))
	var count int
	if err := row.Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrSprintOverlap
	}
	return nil
}

func (s *Service) generateUniqueTicketID(ctx context.Context, tx *sql.Tx, epicName string, ticketType TicketType) (string, error) {
	base := initials(epicName)
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

func scanTicket(scanner interface{ Scan(dest ...any) error }) (Ticket, error) {
	var ticket Ticket
	var blocked int
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
		&ticket.EpicID,
		&ticket.EpicName,
		&sprintID,
		&ticket.SprintName,
		&ticket.GitHubPRURL,
		&ticket.Description,
		&ticket.Position,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Ticket{}, err
	}
	ticket.Blocked = blocked == 1
	if sprintID.Valid {
		value := sprintID.Int64
		ticket.SprintID = &value
	}
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

func scanSprint(scanner interface{ Scan(dest ...any) error }) (Sprint, error) {
	var sprint Sprint
	var startDate string
	var endDate string
	var createdAt string
	var completedAt sql.NullString
	if err := scanner.Scan(&sprint.ID, &sprint.Name, &sprint.Quarter, &startDate, &endDate, &createdAt, &completedAt); err != nil {
		return Sprint{}, err
	}
	var err error
	sprint.StartDate, err = parseDate(startDate)
	if err != nil {
		return Sprint{}, err
	}
	sprint.EndDate, err = parseDate(endDate)
	if err != nil {
		return Sprint{}, err
	}
	sprint.CreatedAt, err = parseTimestamp(createdAt)
	if err != nil {
		return Sprint{}, err
	}
	if completedAt.Valid {
		parsed, err := parseTimestamp(completedAt.String)
		if err != nil {
			return Sprint{}, err
		}
		sprint.CompletedAt = &parsed
	}
	sprint.StartDate = truncateDate(sprint.StartDate)
	sprint.EndDate = truncateDate(sprint.EndDate)
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

func lookupEpicName(ctx context.Context, tx *sql.Tx, epicID int64) (string, error) {
	var name string
	if err := tx.QueryRowContext(ctx, `SELECT name FROM epics WHERE id = ?`, epicID).Scan(&name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return name, nil
}

func nextPosition(ctx context.Context, tx *sql.Tx, sprintID *int64, status TicketStatus) (int, error) {
	query := `SELECT COALESCE(MAX(position), -1) + 1 FROM tickets WHERE status = ? AND `
	args := []any{string(status)}
	if sprintID != nil {
		query += `sprint_id = ?`
		args = append(args, *sprintID)
	} else {
		query += `sprint_id IS NULL`
	}
	var next int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&next); err != nil {
		return 0, err
	}
	return next, nil
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

func ratio(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b) * 100
}

func divide(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(a) / float64(b)
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

func sameOptionalID(a, b *int64) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

func hashPayload(payload SprintWebhookPayload) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
