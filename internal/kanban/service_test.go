package kanban

import (
	"context"
	"encoding/json"
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

func seedEpic(t *testing.T, service *Service, title string) Epic {
	t.Helper()
	epic, err := service.CreateEpic(context.Background(), CreateEpicInput{Title: title, Status: EpicStatusOpen})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	return epic
}

func seedSprint(t *testing.T, service *Service, name string, status SprintStatus, start, end time.Time) Sprint {
	t.Helper()
	sprint, err := service.CreateSprint(context.Background(), CreateSprintInput{
		Name:     name,
		Goal:     "Ship it",
		Status:   status,
		StartsOn: start,
		EndsOn:   end,
	})
	if err != nil {
		t.Fatalf("CreateSprint returned error: %v", err)
	}
	return sprint
}

func ptrInt64(v int64) *int64 { return &v }

func TestCreateAndResolveCurrentSprintFromMembership(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epic := seedEpic(t, service, "Platform")
	active := seedSprint(t, service, "Sprint A", SprintStatusActive, time.Date(2026, 4, 20, 0, 0, 0, 0, time.Local), time.Date(2026, 4, 26, 0, 0, 0, 0, time.Local))

	ticket, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Implement membership",
		Status:      TicketStatusInProgress,
		Type:        TicketTypeFeature,
		StoryPoints: 3,
		EpicID:      &epic.ID,
		SprintID:    &active.ID,
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}

	if ticket.CurrentSprintID == nil || *ticket.CurrentSprintID != active.ID {
		t.Fatalf("expected current sprint %d, got %+v", active.ID, ticket.CurrentSprintID)
	}
	if len(ticket.SprintHistory) != 1 {
		t.Fatalf("expected one sprint history row, got %d", len(ticket.SprintHistory))
	}
}

func TestActivateBlocksWhenAnotherSprintIsActive(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	_ = seedSprint(t, service, "Sprint A", SprintStatusActive, time.Date(2026, 4, 20, 0, 0, 0, 0, time.Local), time.Date(2026, 4, 26, 0, 0, 0, 0, time.Local))
	planned := seedSprint(t, service, "Sprint B", SprintStatusPlanned, time.Date(2026, 4, 27, 0, 0, 0, 0, time.Local), time.Date(2026, 5, 3, 0, 0, 0, 0, time.Local))

	_, err := service.ActivateSprint(context.Background(), planned.ID)
	if err == nil || !strings.Contains(err.Error(), ErrActiveSprintExists.Error()) {
		t.Fatalf("expected active sprint conflict, got %v", err)
	}
}

func TestCloseSprintCarriesSelectedIncompleteTickets(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epic := seedEpic(t, service, "Platform")
	active := seedSprint(t, service, "Sprint A", SprintStatusActive, time.Date(2026, 4, 20, 0, 0, 0, 0, time.Local), time.Date(2026, 4, 26, 0, 0, 0, 0, time.Local))

	keep, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Carry me",
		Status:      TicketStatusInProgress,
		Type:        TicketTypeFeature,
		StoryPoints: 2,
		EpicID:      &epic.ID,
		SprintID:    &active.ID,
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	_, err = service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Leave me",
		Status:      TicketStatusDone,
		Type:        TicketTypeFeature,
		StoryPoints: 1,
		EpicID:      &epic.ID,
		SprintID:    &active.ID,
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}

	_, err = service.CloseSprint(context.Background(), active.ID, CloseSprintInput{
		CreateNextSprint: &CreateSprintInput{
			Name:     "Sprint B",
			Goal:     "Next",
			StartsOn: time.Date(2026, 4, 27, 0, 0, 0, 0, time.Local),
			EndsOn:   time.Date(2026, 5, 3, 0, 0, 0, 0, time.Local),
		},
		CarryOverTicketIDs: []string{keep.TicketID},
	})
	if err != nil {
		t.Fatalf("CloseSprint returned error: %v", err)
	}

	activeAfter, err := service.GetActiveSprint(context.Background())
	if err != nil {
		t.Fatalf("GetActiveSprint returned error: %v", err)
	}
	if activeAfter.Name != "Sprint B" {
		t.Fatalf("expected Sprint B active, got %s", activeAfter.Name)
	}

	kept, err := service.GetTicketDetail(context.Background(), keep.TicketID)
	if err != nil {
		t.Fatalf("GetTicketDetail returned error: %v", err)
	}
	if kept.CurrentSprintName != "Sprint B" {
		t.Fatalf("expected carried ticket in Sprint B, got %s", kept.CurrentSprintName)
	}
	if len(kept.SprintHistory) != 2 {
		t.Fatalf("expected two sprint history rows, got %d", len(kept.SprintHistory))
	}
}

func TestEpicAssignAndClear(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epicA := seedEpic(t, service, "Platform")
	epicB := seedEpic(t, service, "CLI")
	ticket, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "No epic by default",
		Status:      TicketStatusNotStarted,
		Type:        TicketTypeFeature,
		StoryPoints: 1,
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	if ticket.EpicID != nil {
		t.Fatalf("expected nil epic by default, got %v", *ticket.EpicID)
	}

	ticket, err = service.AssignEpicToTicket(context.Background(), ticket.TicketID, epicA.ID)
	if err != nil {
		t.Fatalf("AssignEpicToTicket returned error: %v", err)
	}
	if ticket.EpicID == nil || *ticket.EpicID != epicA.ID {
		t.Fatalf("expected epic %d, got %+v", epicA.ID, ticket.EpicID)
	}

	ticket, err = service.AssignEpicToTicket(context.Background(), ticket.TicketID, epicB.ID)
	if err != nil {
		t.Fatalf("AssignEpicToTicket returned error: %v", err)
	}
	if ticket.EpicID == nil || *ticket.EpicID != epicB.ID {
		t.Fatalf("expected epic %d, got %+v", epicB.ID, ticket.EpicID)
	}

	ticket, err = service.ClearEpicForTicket(context.Background(), ticket.TicketID)
	if err != nil {
		t.Fatalf("ClearEpicForTicket returned error: %v", err)
	}
	if ticket.EpicID != nil {
		t.Fatalf("expected nil epic after clear, got %+v", ticket.EpicID)
	}
}

func TestDeleteEpicSetsTicketEpicToNull(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	epic := seedEpic(t, service, "Platform")
	ticket, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Bound ticket",
		Status:      TicketStatusNotStarted,
		Type:        TicketTypeFeature,
		StoryPoints: 1,
		EpicID:      &epic.ID,
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}

	if err := service.DeleteEpic(context.Background(), epic.ID); err != nil {
		t.Fatalf("DeleteEpic returned error: %v", err)
	}
	ticket, err = service.GetTicketDetail(context.Background(), ticket.TicketID)
	if err != nil {
		t.Fatalf("GetTicketDetail returned error: %v", err)
	}
	if ticket.EpicID != nil {
		t.Fatalf("expected deleted epic to null ticket epic, got %+v", ticket.EpicID)
	}
}

func TestExportsAndTicketWebhookPayloadIncludeSprintHistory(t *testing.T) {
	t.Parallel()

	var payloads []TicketWebhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload TicketWebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err == nil && payload.Event != "" {
			payloads = append(payloads, payload)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	service := newTestService(t)
	service.config.Webhook.EndpointURL = server.URL
	service.config.Webhook.MaxRetries = 1
	epic := seedEpic(t, service, "Platform")
	sprint := seedSprint(t, service, "Sprint A", SprintStatusActive, time.Date(2026, 4, 20, 0, 0, 0, 0, time.Local), time.Date(2026, 4, 26, 0, 0, 0, 0, time.Local))
	ticket, err := service.CreateTicket(context.Background(), CreateTicketInput{
		Title:       "Webhook me",
		Status:      TicketStatusNotStarted,
		Type:        TicketTypeFeature,
		StoryPoints: 5,
		EpicID:      &epic.ID,
		SprintID:    &sprint.ID,
		Description: "Ticket description",
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}

	mdPath, err := service.ExportTicketMarkdown(context.Background(), ticket.TicketID, "")
	if err != nil {
		t.Fatalf("ExportTicketMarkdown returned error: %v", err)
	}
	mdData, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(mdData), "## Sprint History") {
		t.Fatalf("expected sprint history in markdown export, got %q", string(mdData))
	}

	csvPath, err := service.ExportTicketCSV(context.Background(), ticket.TicketID, "")
	if err != nil {
		t.Fatalf("ExportTicketCSV returned error: %v", err)
	}
	csvData, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(csvData), "sprint_history_json") {
		t.Fatalf("expected sprint_history_json in CSV export, got %q", string(csvData))
	}

	if len(payloads) == 0 {
		t.Fatal("expected ticket webhook payload")
	}
	last := payloads[len(payloads)-1]
	if last.Ticket.CurrentSprintID == nil || *last.Ticket.CurrentSprintID != sprint.ID {
		t.Fatalf("expected current sprint in payload, got %+v", last.Ticket.CurrentSprintID)
	}
	if len(last.Ticket.SprintHistory) == 0 {
		t.Fatalf("expected sprint history in payload, got %+v", last.Ticket.SprintHistory)
	}
}

func TestSprintEndWebhookIsIdempotent(t *testing.T) {
	t.Parallel()

	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	service := newTestService(t)
	service.config.Webhook.EndpointURL = server.URL
	service.config.Webhook.MaxRetries = 1
	sprint := seedSprint(t, service, "Closed Sprint", SprintStatusClosed, time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local), time.Date(2026, 4, 7, 0, 0, 0, 0, time.Local))

	results, err := service.PostSprintEndWebhooks(context.Background(), sprint.ID, false)
	if err != nil {
		t.Fatalf("PostSprintEndWebhooks returned error: %v", err)
	}
	if len(results) != 1 || results[0].Skipped {
		t.Fatalf("expected a posted result, got %+v", results)
	}

	results, err = service.PostSprintEndWebhooks(context.Background(), sprint.ID, false)
	if err != nil {
		t.Fatalf("PostSprintEndWebhooks second call returned error: %v", err)
	}
	if len(results) != 1 || !results[0].Skipped {
		t.Fatalf("expected skipped result on second call, got %+v", results)
	}
	if posts.Load() != 1 {
		t.Fatalf("expected one POST, got %d", posts.Load())
	}
}
