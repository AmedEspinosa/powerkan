package kanban

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amedespinosa/powerkan/internal/config"
	"github.com/amedespinosa/powerkan/internal/storage"
)

func newTestService(t *testing.T) *Service {
	t.Helper()

	cfg := config.Defaults(filepath.Join(t.TempDir(), "exports"))
	cfg.App.Timezone = "America/New_York"

	dbPath := filepath.Join(t.TempDir(), "kanban.db")
	db, err := storage.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyMigrations returned error: %v", err)
	}

	service := NewService(db, cfg)
	service.SetNowFunc(func() time.Time {
		return time.Date(2026, 4, 24, 10, 15, 0, 0, time.FixedZone("EDT", -4*60*60))
	})
	return service
}

func seedEpicAndSprint(t *testing.T, service *Service) (Epic, Sprint) {
	t.Helper()
	epic, err := service.CreateEpic(context.Background(), CreateEpicInput{Name: "Lemon Squeezer Backend"})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	sprint, err := service.CreateSprint(context.Background(), CreateSprintInput{
		Name:      "26Q2 Sprint 2",
		Quarter:   "2026Q2",
		StartDate: time.Date(2026, 4, 20, 0, 0, 0, 0, time.Local),
		EndDate:   time.Date(2026, 4, 26, 0, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateSprint returned error: %v", err)
	}
	return epic, sprint
}

func TestCreateTicketGeneratesStructuredIDAndCommentsNewestFirst(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epic, sprint := seedEpicAndSprint(t, service)

	ticket, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Implement board",
		Status:      TicketStatusInProgress,
		Type:        TicketTypeFeature,
		Blocked:     true,
		StoryPoints: 8,
		EpicID:      epic.ID,
		SprintID:    &sprint.ID,
		Description: "Board work",
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	if !strings.HasPrefix(ticket.TicketID, "LSB-FEA-2604241015") {
		t.Fatalf("expected generated ticket ID prefix, got %q", ticket.TicketID)
	}

	service.SetNowFunc(func() time.Time {
		return time.Date(2026, 4, 24, 10, 16, 0, 0, time.FixedZone("EDT", -4*60*60))
	})
	if _, err := service.AddComment(context.Background(), ticket.TicketID, AddCommentInput{Kind: CommentKindText, Body: "First"}); err != nil {
		t.Fatalf("AddComment returned error: %v", err)
	}
	service.SetNowFunc(func() time.Time {
		return time.Date(2026, 4, 24, 10, 17, 0, 0, time.FixedZone("EDT", -4*60*60))
	})
	if _, err := service.AddComment(context.Background(), ticket.TicketID, AddCommentInput{Kind: CommentKindURL, Body: "https://example.com"}); err != nil {
		t.Fatalf("AddComment returned error: %v", err)
	}

	detail, err := service.GetTicketDetail(context.Background(), ticket.TicketID)
	if err != nil {
		t.Fatalf("GetTicketDetail returned error: %v", err)
	}
	if len(detail.Comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(detail.Comments))
	}
	if detail.Comments[0].Body != "https://example.com" {
		t.Fatalf("expected newest comment first, got %q", detail.Comments[0].Body)
	}
}

func TestCreateSprintRejectsOverlap(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	_, _ = seedEpicAndSprint(t, service)

	_, err := service.CreateSprint(context.Background(), CreateSprintInput{
		Name:      "26Q2 Sprint 3",
		Quarter:   "2026Q2",
		StartDate: time.Date(2026, 4, 25, 0, 0, 0, 0, time.Local),
		EndDate:   time.Date(2026, 5, 1, 0, 0, 0, 0, time.Local),
	})
	if err == nil {
		t.Fatal("expected overlap error")
	}
	if err != ErrSprintOverlap {
		t.Fatalf("expected ErrSprintOverlap, got %v", err)
	}
}

func TestListTicketsFiltersAndTotalPoints(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epic, sprint := seedEpicAndSprint(t, service)

	secondEpic, err := service.CreateEpic(context.Background(), CreateEpicInput{Name: "CLI Platform"})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	if _, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "In sprint",
		Status:      TicketStatusDone,
		Type:        TicketTypeFeature,
		StoryPoints: 5,
		EpicID:      epic.ID,
		SprintID:    &sprint.ID,
	}); err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	if _, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Backlog",
		Status:      TicketStatusNotStarted,
		Type:        TicketTypeBug,
		StoryPoints: 3,
		EpicID:      secondEpic.ID,
	}); err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}

	result, err := service.ListTickets(context.Background(), TicketListFilters{SprintID: &sprint.ID, EpicID: &epic.ID})
	if err != nil {
		t.Fatalf("ListTickets returned error: %v", err)
	}
	if len(result.Tickets) != 1 {
		t.Fatalf("expected 1 filtered ticket, got %d", len(result.Tickets))
	}
	if result.TotalPoints != 5 {
		t.Fatalf("expected filtered total points 5, got %d", result.TotalPoints)
	}
}

func TestBoardMetricsAndReorder(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epic, sprint := seedEpicAndSprint(t, service)

	first, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "First",
		Status:      TicketStatusNotStarted,
		Type:        TicketTypeFeature,
		StoryPoints: 5,
		EpicID:      epic.ID,
		SprintID:    &sprint.ID,
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	second, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Second",
		Status:      TicketStatusNotStarted,
		Type:        TicketTypeFix,
		StoryPoints: 8,
		EpicID:      epic.ID,
		SprintID:    &sprint.ID,
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}

	board, err := service.LoadBoard(context.Background())
	if err != nil {
		t.Fatalf("LoadBoard returned error: %v", err)
	}
	if board.Metrics.DaysLeft != 3 {
		t.Fatalf("expected 3 days left on 2026-04-24 for sprint ending 2026-04-26, got %d", board.Metrics.DaysLeft)
	}
	if board.Metrics.PointsPerDay != 1.8571428571428572 {
		t.Fatalf("unexpected points per day: %f", board.Metrics.PointsPerDay)
	}
	if _, err := service.ReorderTicket(context.Background(), second.TicketID, -1); err != nil {
		t.Fatalf("ReorderTicket returned error: %v", err)
	}
	board, err = service.LoadBoard(context.Background())
	if err != nil {
		t.Fatalf("LoadBoard returned error: %v", err)
	}
	if len(board.Columns[0].Tickets) != 2 {
		t.Fatalf("expected 2 tickets in first column, got %d", len(board.Columns[0].Tickets))
	}
	if board.Columns[0].Tickets[0].TicketID != second.TicketID {
		t.Fatalf("expected reordered ticket first, got %q", board.Columns[0].Tickets[0].TicketID)
	}
	if _, err := service.MoveTicket(context.Background(), first.TicketID, 1); err != nil {
		t.Fatalf("MoveTicket returned error: %v", err)
	}
	board, err = service.LoadBoard(context.Background())
	if err != nil {
		t.Fatalf("LoadBoard returned error: %v", err)
	}
	if len(board.Columns[1].Tickets) != 1 {
		t.Fatalf("expected moved ticket in second column, got %d", len(board.Columns[1].Tickets))
	}
}

func TestExportTicketMarkdownAndCSV(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epic, sprint := seedEpicAndSprint(t, service)

	ticket, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Export me",
		Status:      TicketStatusDone,
		Type:        TicketTypeDocs,
		StoryPoints: 2,
		EpicID:      epic.ID,
		SprintID:    &sprint.ID,
		Description: "Ready for export",
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	if _, err := service.AddComment(context.Background(), ticket.TicketID, AddCommentInput{Kind: CommentKindText, Body: "Looks good"}); err != nil {
		t.Fatalf("AddComment returned error: %v", err)
	}

	mdPath, err := service.ExportTicketMarkdown(context.Background(), ticket.TicketID, "")
	if err != nil {
		t.Fatalf("ExportTicketMarkdown returned error: %v", err)
	}
	mdData, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(mdData), "## Comments") {
		t.Fatalf("expected markdown comments section, got %q", string(mdData))
	}

	csvPath, err := service.ExportTicketCSV(context.Background(), ticket.TicketID, "")
	if err != nil {
		t.Fatalf("ExportTicketCSV returned error: %v", err)
	}
	csvData, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(csvData), "ticket_id,title,status") {
		t.Fatalf("expected CSV header row, got %q", string(csvData))
	}
}

func TestPostSprintEndWebhooksIsIdempotentUnlessForced(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epic, sprint := seedEpicAndSprint(t, service)
	if _, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Done ticket",
		Status:      TicketStatusDone,
		Type:        TicketTypeFeature,
		StoryPoints: 5,
		EpicID:      epic.ID,
		SprintID:    &sprint.ID,
	}); err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	service.SetNowFunc(func() time.Time {
		return time.Date(2026, 4, 28, 9, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	})

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	service.config.Webhook.EndpointURL = server.URL
	service.SetHTTPClient(server.Client())

	results, err := service.PostSprintEndWebhooks(context.Background(), 0, false)
	if err != nil {
		t.Fatalf("PostSprintEndWebhooks returned error: %v", err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("expected one posted webhook, got %+v", results)
	}

	results, err = service.PostSprintEndWebhooks(context.Background(), 0, false)
	if err != nil {
		t.Fatalf("second PostSprintEndWebhooks returned error: %v", err)
	}
	if len(results) != 1 || !results[0].Skipped {
		t.Fatalf("expected skipped duplicate, got %+v", results)
	}

	if _, err := service.PostSprintEndWebhooks(context.Background(), sprint.ID, true); err != nil {
		t.Fatalf("forced PostSprintEndWebhooks returned error: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("expected 2 HTTP posts, got %d", requests.Load())
	}
}
