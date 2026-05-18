package kanban

import "time"

type TicketStatus string

const (
	TicketStatusNotStarted  TicketStatus = "NOT_STARTED"
	TicketStatusInProgress  TicketStatus = "IN_PROGRESS"
	TicketStatusUnderReview TicketStatus = "UNDER_REVIEW"
	TicketStatusDone        TicketStatus = "DONE"
)

var TicketStatuses = []TicketStatus{
	TicketStatusNotStarted,
	TicketStatusInProgress,
	TicketStatusUnderReview,
	TicketStatusDone,
}

type TicketType string

const (
	TicketTypeFeature TicketType = "FEATURE"
	TicketTypeBug     TicketType = "BUG"
	TicketTypeFix     TicketType = "FIX"
	TicketTypeDocs    TicketType = "DOCS"
)

var TicketTypes = []TicketType{
	TicketTypeFeature,
	TicketTypeBug,
	TicketTypeFix,
	TicketTypeDocs,
}

type CommentKind string

const (
	CommentKindText     CommentKind = "TEXT"
	CommentKindURL      CommentKind = "URL"
	CommentKindFilePath CommentKind = "FILE_PATH"
)

var CommentKinds = []CommentKind{
	CommentKindText,
	CommentKindURL,
	CommentKindFilePath,
}

type SprintStatus string

const (
	SprintStatusPlanned SprintStatus = "planned"
	SprintStatusActive  SprintStatus = "active"
	SprintStatusClosed  SprintStatus = "closed"
)

var SprintStatuses = []SprintStatus{
	SprintStatusPlanned,
	SprintStatusActive,
	SprintStatusClosed,
}

type EpicStatus string

const (
	EpicStatusOpen EpicStatus = "open"
	EpicStatusDone EpicStatus = "done"
)

var EpicStatuses = []EpicStatus{
	EpicStatusOpen,
	EpicStatusDone,
}

type Epic struct {
	ID        int64
	Title     string
	Name      string
	Status    EpicStatus
	Color     *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Sprint struct {
	ID        int64
	Name      string
	Goal      string
	Quarter   string
	Status    SprintStatus
	StartsOn  time.Time
	EndsOn    time.Time
	StartDate time.Time
	EndDate   time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	ClosedAt  *time.Time
	CompletedAt *time.Time
}

type SprintTicket struct {
	SprintID    int64
	SprintName  string
	SprintStatus SprintStatus
	AddedAt     time.Time
	RemovedAt   *time.Time
}

type Ticket struct {
	ID                int64
	TicketID          string
	Title             string
	Status            TicketStatus
	Type              TicketType
	Blocked           bool
	StoryPoints       float64
	EpicID            *int64
	EpicTitle         string
	EpicName          string
	EpicStatus        EpicStatus
	EpicColor         *string
	CurrentSprintID   *int64
	CurrentSprintName string
	SprintID          *int64
	SprintName        string
	GitHubPRURL       string
	Description       string
	Position          int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	SprintHistory     []SprintTicket
}

type TicketComment struct {
	ID        int64
	TicketRef int64
	Kind      CommentKind
	Body      string
	CreatedAt time.Time
}

type TicketDetail struct {
	Ticket
	Comments []TicketComment
}

type BoardColumn struct {
	Status  TicketStatus
	Tickets []Ticket
}

type BoardScope string

const (
	BoardScopeSprint  BoardScope = "sprint"
	BoardScopeBacklog BoardScope = "backlog"
)

type BoardMetrics struct {
	Sprint           *Sprint
	DaysLeft         int
	PercentCompleted float64
	PointsPerDay     float64
	TotalPoints      float64
	DonePoints       float64
	Scope            BoardScope
}

type BoardData struct {
	Metrics BoardMetrics
	Columns []BoardColumn
}

type TicketListFilters struct {
	SprintID *int64
	Backlog  bool
	EpicID   *int64
	Status   *TicketStatus
}

type TicketListResult struct {
	Tickets      []Ticket
	TotalPoints  float64
	Epics        []Epic
	Sprints      []Sprint
	ActiveFilter TicketListFilters
}

type SprintListFilters struct {
	Status *SprintStatus
}

type SprintSummary struct {
	Sprint
	PercentCompleted float64
	PointsCompleted  float64
	TotalPoints      float64
	TicketCount      int
}

type CreateEpicInput struct {
	Name   string
	Title  string
	Status EpicStatus
	Color  *string
}

type UpdateEpicInput struct {
	Name   string
	Title  string
	Status EpicStatus
	Color  *string
}

type CreateSprintInput struct {
	Name     string
	Goal     string
	Quarter  string
	Status   SprintStatus
	StartsOn time.Time
	EndsOn   time.Time
	StartDate time.Time
	EndDate   time.Time
}

type UpdateSprintInput struct {
	Name     string
	Goal     string
	Quarter  string
	Status   SprintStatus
	StartsOn time.Time
	EndsOn   time.Time
	StartDate time.Time
	EndDate   time.Time
}

type CloseSprintInput struct {
	NextSprintID       *int64
	CreateNextSprint   *CreateSprintInput
	CarryOverTicketIDs []string
}

type CreateTicketInput struct {
	Title       string
	Status      TicketStatus
	Type        TicketType
	Blocked     bool
	StoryPoints float64
	EpicID      *int64
	SprintID    *int64
	GitHubPRURL string
	Description string
}

type UpdateTicketInput struct {
	Title       string
	Status      TicketStatus
	Type        TicketType
	Blocked     bool
	StoryPoints float64
	EpicID      *int64
	SprintID    *int64
	GitHubPRURL string
	Description string
}

type AddCommentInput struct {
	Kind CommentKind
	Body string
}

type ExportTicketData struct {
	TicketDetail
}

type SprintWebhookPayload struct {
	StartDate        string  `json:"start_date"`
	EndDate          string  `json:"end_date"`
	TotalPoints      float64 `json:"total_points"`
	PointsCompleted  float64 `json:"points_completed"`
	PercentCompleted float64 `json:"percent_completed"`
}

type TicketWebhookPayload struct {
	Event  string            `json:"event"`
	Ticket TicketWebhookData `json:"ticket"`
}

type TicketWebhookData struct {
	TicketID         string                    `json:"ticket_id"`
	Title            string                    `json:"title"`
	Status           TicketStatus              `json:"status"`
	Type             TicketType                `json:"type"`
	Blocked          bool                      `json:"blocked"`
	StoryPoints      float64                   `json:"story_points"`
	EpicID           *int64                    `json:"epic_id"`
	Epic             *TicketWebhookEpic        `json:"epic,omitempty"`
	CurrentSprintID  *int64                    `json:"current_sprint_id"`
	CurrentSprint    *TicketWebhookSprint      `json:"current_sprint,omitempty"`
	SprintHistory    []TicketWebhookSprintLink `json:"sprint_history"`
	GitHubPRURL      string                    `json:"github_pr_url,omitempty"`
	Description      string                    `json:"description,omitempty"`
	CreatedAt        string                    `json:"created_at"`
	UpdatedAt        string                    `json:"updated_at"`
}

type TicketWebhookEpic struct {
	ID     int64      `json:"id"`
	Title  string     `json:"title"`
	Status EpicStatus `json:"status"`
	Color  *string    `json:"color,omitempty"`
}

type TicketWebhookSprint struct {
	ID     int64        `json:"id"`
	Name   string       `json:"name"`
	Status SprintStatus `json:"status"`
}

type TicketWebhookSprintLink struct {
	SprintID   int64        `json:"sprint_id"`
	SprintName string       `json:"sprint_name"`
	Status     SprintStatus `json:"status"`
	AddedAt    string       `json:"added_at"`
	RemovedAt  *string      `json:"removed_at,omitempty"`
}

type WebhookPostResult struct {
	SprintID int64
	Payload  SprintWebhookPayload
	Skipped  bool
}
