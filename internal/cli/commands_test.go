package cli

import (
	"bytes"
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
	"github.com/amedespinosa/powerkan/internal/kanban"
	"github.com/amedespinosa/powerkan/internal/platform"
	"github.com/amedespinosa/powerkan/internal/storage"
)

func seedCLIEnvironment(t *testing.T, configContents string) (platform.Paths, *kanban.Service) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	paths, err := platform.ResolvePaths("")
	if err != nil {
		t.Fatalf("ResolvePaths returned error: %v", err)
	}
	if err := platform.EnsureDirectories(paths); err != nil {
		t.Fatalf("EnsureDirectories returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(configContents), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	db, err := storage.Open(context.Background(), paths.DatabaseFile)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := storage.ApplyMigrations(context.Background(), db); err != nil {
		t.Fatalf("ApplyMigrations returned error: %v", err)
	}
	cfg, err := config.Load(paths.ConfigFile, paths.ExportsDir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	service := kanban.NewService(db, cfg)
	service.SetNowFunc(func() time.Time {
		return time.Date(2026, 4, 28, 9, 0, 0, 0, time.FixedZone("EDT", -4*60*60))
	})
	return paths, service
}

func seedTicketData(t *testing.T, service *kanban.Service) kanban.TicketDetail {
	t.Helper()
	epic, err := service.CreateEpic(context.Background(), kanban.CreateEpicInput{Name: "Lemon Squeezer Backend"})
	if err != nil {
		t.Fatalf("CreateEpic returned error: %v", err)
	}
	sprint, err := service.CreateSprint(context.Background(), kanban.CreateSprintInput{
		Name:      "26Q2 Sprint 2",
		Quarter:   "2026Q2",
		StartDate: time.Date(2024, 4, 20, 0, 0, 0, 0, time.Local),
		EndDate:   time.Date(2024, 4, 26, 0, 0, 0, 0, time.Local),
	})
	if err != nil {
		t.Fatalf("CreateSprint returned error: %v", err)
	}
	ticket, err := service.CreateTicket(context.Background(), kanban.CreateTicketInput{
		Title:       "Exportable ticket",
		Status:      kanban.TicketStatusDone,
		Type:        kanban.TicketTypeFeature,
		StoryPoints: 5,
		EpicID:      epic.ID,
		SprintID:    &sprint.ID,
		Description: "Ticket description",
	})
	if err != nil {
		t.Fatalf("CreateTicket returned error: %v", err)
	}
	if _, err := service.AddComment(context.Background(), ticket.TicketID, kanban.AddCommentInput{Kind: kanban.CommentKindText, Body: "Shipped"}); err != nil {
		t.Fatalf("AddComment returned error: %v", err)
	}
	return ticket
}

func TestExportTicketCommandWritesDefaultMarkdownFile(t *testing.T) {
	paths, service := seedCLIEnvironment(t, "app:\n  timezone: America/New_York\n")
	ticket := seedTicketData(t, service)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"export", "ticket", "--id", ticket.TicketID, "--format", "md"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	expectedPath := filepath.Join(paths.ExportsDir, ticket.TicketID+".md")
	data, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !strings.Contains(string(data), "## Comments") {
		t.Fatalf("expected markdown export content, got %q", string(data))
	}
	if !strings.Contains(stdout.String(), expectedPath) {
		t.Fatalf("expected command output to print export path, got %q", stdout.String())
	}
}

func TestWebhookSprintEndCommandPostsAndPrintsResult(t *testing.T) {
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, service := seedCLIEnvironment(t, "app:\n  timezone: America/New_York\nwebhook:\n  endpoint_url: "+server.URL+"\n  timeout_seconds: 2\n  max_retries: 1\n  retry_backoff_seconds: 1\n")
	_ = seedTicketData(t, service)

	cmd := NewRootCommand()
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"webhook", "sprint-end"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if posts.Load() != 1 {
		t.Fatalf("expected 1 webhook POST, got %d", posts.Load())
	}
	if !strings.Contains(stdout.String(), "posted sprint") {
		t.Fatalf("expected command output to mention posted sprint, got %q", stdout.String())
	}
}
