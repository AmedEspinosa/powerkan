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
	SprintStatusNotStarted SprintStatus = "NOT_STARTED"
	SprintStatusInProgress SprintStatus = "IN_PROGRESS"
	SprintStatusDone       SprintStatus = "DONE"
)

var SprintStatuses = []SprintStatus{
	SprintStatusNotStarted,
	SprintStatusInProgress,
	SprintStatusDone,
}

type Epic struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

type Sprint struct {
	ID          int64
	Name        string
	Quarter     string
	StartDate   time.Time
	EndDate     time.Time
	CreatedAt   time.Time
	CompletedAt *time.Time
	Status      SprintStatus
}

type Ticket struct {
	ID          int64
	TicketID    string
	Title       string
	Status      TicketStatus
	Type        TicketType
	Blocked     bool
	StoryPoints int
	EpicID      int64
	EpicName    string
	SprintID    *int64
	SprintName  string
	GitHubPRURL string
	Description string
	Position    int
	CreatedAt   time.Time
	UpdatedAt   time.Time
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

type BoardMetrics struct {
	Sprint           *Sprint
	DaysLeft         int
	PercentCompleted float64
	PointsPerDay     float64
	TotalPoints      int
	DonePoints       int
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
	TotalPoints  int
	Epics        []Epic
	Sprints      []Sprint
	ActiveFilter TicketListFilters
}

type SprintListFilters struct {
	Quarter *string
	Status  *SprintStatus
}

type SprintSummary struct {
	Sprint
	PercentCompleted float64
	PointsCompleted  int
	TotalPoints      int
	TicketCount      int
}

type CreateEpicInput struct {
	Name string
}

type CreateSprintInput struct {
	Name      string
	Quarter   string
	StartDate time.Time
	EndDate   time.Time
}

type UpdateSprintInput struct {
	Name      string
	Quarter   string
	StartDate time.Time
	EndDate   time.Time
}

type CreateTicketInput struct {
	Title       string
	Status      TicketStatus
	Type        TicketType
	Blocked     bool
	StoryPoints int
	EpicID      int64
	SprintID    *int64
	GitHubPRURL string
	Description string
}

type UpdateTicketInput struct {
	Title       string
	Status      TicketStatus
	Type        TicketType
	Blocked     bool
	StoryPoints int
	EpicID      int64
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
	TotalPoints      int     `json:"total_points"`
	PointsCompleted  int     `json:"points_completed"`
	PercentCompleted float64 `json:"percent_completed"`
}

type WebhookPostResult struct {
	SprintID int64
	Payload  SprintWebhookPayload
	Skipped  bool
}
