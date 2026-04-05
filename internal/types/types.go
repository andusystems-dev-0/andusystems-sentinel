// Package types defines all shared data structures used across sentinel packages.
package types

import "time"

// ---- PR / Sentinel PR -------------------------------------------------------

type SentinelPR struct {
	ID               string
	Repo             string
	PRNumber         int
	PRUrl            string
	Branch           string
	BaseBranch       string
	Title            string
	PRType           string
	PriorityTier     PRPriorityTier
	RelatedPRNumber  int
	DiscordMessageID string
	DiscordChannelID string
	DiscordThreadID  string
	Status           PRStatus
	OpenedAt         time.Time
	ResolvedAt       *time.Time
	ResolvedBy       string
	TaskID           string
}

type PRStatus string

const (
	PRStatusOpen   PRStatus = "open"
	PRStatusMerged PRStatus = "merged"
	PRStatusClosed PRStatus = "closed"
)

type PRPriorityTier string

const (
	PRTierHigh PRPriorityTier = "high"
	PRTierLow  PRPriorityTier = "low"
)

// ---- Webhook events ---------------------------------------------------------

type ForgejoEvent struct {
	Type       string // "pull_request" | "push" | "issue_comment"
	Repo       string
	Payload    []byte
	ReceivedAt time.Time
}

// ---- Digest -----------------------------------------------------------------

type NightlyDigest struct {
	HighPriorityPRs []SentinelPR
	LowPriorityPRs  []SentinelPR
	PendingFindings []PendingFindingDigest
}

type PendingFindingDigest struct {
	Repo          string
	Count         int
	AffectedFiles []string
	OldestAge     time.Duration
}

// ---- Sanitization -----------------------------------------------------------

type SanitizationFinding struct {
	ID                   string
	SyncRunID            string
	Layer                int
	Repo                 string
	Filename             string
	LineNumber           int
	ByteOffsetStart      int
	ByteOffsetEnd        int
	OriginalValue        string // Never logged; accessed only inside per-file mutex
	SuggestedReplacement string
	Category             string
	Confidence           string // "high" | "medium" | "low"
	AutoRedacted         bool
	TokenIndex           int // 0 for auto-redacted
	PendingResolutionID  string
}

type PendingResolution struct {
	ID                   string
	Repo                 string
	Filename             string
	FindingID            string
	SyncRunID            string
	ForgejoIssueNumber   int
	DiscordMessageID     string
	DiscordChannelID     string
	DiscordThreadID      string
	HasThread            bool
	SuggestedReplacement string
	Status               ResolutionStatus
	SupersededBy         string
	ResolvedAt           *time.Time
	ResolvedBy           string
	FinalValue           string
}

type ResolutionStatus string

const (
	StatusPending        ResolutionStatus = "pending"
	StatusApproved       ResolutionStatus = "approved"
	StatusRejected       ResolutionStatus = "rejected"
	StatusCustomReplaced ResolutionStatus = "custom_replaced"
	StatusReanalyzing    ResolutionStatus = "reanalyzing"
	StatusSuperseded     ResolutionStatus = "superseded"
)

type SkipZone struct{ Start, End int }

type ApprovedValue struct {
	ID         string
	Repo       string
	Value      string
	Category   string
	ApprovedBy string
	ApprovedAt time.Time
}

// ---- Tasks ------------------------------------------------------------------

type TaskSpec struct {
	ID                 string
	Type               string // "bug" | "vulnerability" | "docs" | "dependency-update" | "feature" | "refactor" | "issue"
	Priority           string // "1" (highest) through "5"
	Complexity         string // "trivial" | "small" | "medium" | "large"
	Title              string
	AffectedFiles      []string
	Description        string
	AcceptanceCriteria []string
	ContextNotes       string
}

type TaskResult struct {
	Success bool
	Output  string
	Error   string
}

// ---- LLM options ------------------------------------------------------------

type AnalyzeOpts struct {
	Repo       string
	FileBatch  []FileDiff // One batch <= context_window - response_buffer tokens
	FocusAreas []string
}

type ReviewOpts struct {
	Repo       string
	PRNumber   int
	PRTitle    string
	Diff       string
	BaseBranch string
}

type ProseOpts struct {
	Role    string // 'issue', 'readme', 'dependency_pr', 'release_notes', 'pr_summary'
	Context string
}

type ThreadOpts struct {
	Role     string // 'finding' or 'pr'
	Context  string // Finding detail + file chunk, or PR title + diff summary
	Question string
}

type HousekeepingOpts struct {
	PRTitle           string
	DiffSummary       string
	HousekeepingFiles []string
}

// ---- Review results ---------------------------------------------------------

type ReviewResult struct {
	Verdict            string // "APPROVE" | "REQUEST_CHANGES" | "COMMENT"
	PerFileNotes       []FileNote
	SecurityAssessment string
	TestAssessment     string
	ChangelogText      string
	DocUpdates         map[string]string // filename → updated content
	HousekeepingFiles  map[string][]byte // filename → content for companion PR
	IssueSpecs         []IssueSpec
	CodeFixNeeded      bool
	CodeFixDescription string
}

type FileNote struct {
	Filename string
	Notes    string
	Severity string // "info" | "warning" | "critical"
}

type IssueSpec struct {
	Title  string
	Body   string
	Labels []string
}

type IssueOptions struct {
	Title  string
	Body   string
	Labels []string
}

// ---- Forgejo ----------------------------------------------------------------

type ForgejoPR struct {
	Number   int
	Title    string
	URL      string
	HeadRef  string
	BaseRef  string
	State    string
	MergedAt *time.Time
}

type FileDiff struct {
	Filename     string
	Diff         string
	LinesAdded   int
	LinesRemoved int
	EstTokens    int
}

// ---- Migration --------------------------------------------------------------

type MigrationState struct {
	Repo        string
	Status      string
	ForgejoSHA  string
	StartedAt   *time.Time
	CompletedAt *time.Time
	Error       string
}

// ---- Sanitize opts/result ---------------------------------------------------

type SanitizeFileOpts struct {
	Repo      string
	Filename  string
	Content   []byte
	SkipZones []SkipZone
	SyncRunID string
}

type SanitizeFileResult struct {
	SanitizedContent []byte
	Findings         []SanitizationFinding
}

// ---- Sync run ---------------------------------------------------------------

type SyncRun struct {
	ID               string
	Repo             string
	Mode             int // 3 or 4
	Status           string
	StartedAt        time.Time
	CompletedAt      *time.Time
	FilesSynced      int
	FilesWithPending int
	FindingsHigh     int
	FindingsMedium   int
	FindingsLow      int
}

// ---- PR creation ------------------------------------------------------------

type OpenPROptions struct {
	Repo            string
	Branch          string
	BaseBranch      string
	Title           string
	Body            string
	Labels          []string
	PRType          string
	PriorityTier    PRPriorityTier
	RelatedPRNumber int
}
