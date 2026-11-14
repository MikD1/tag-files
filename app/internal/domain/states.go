package domain

// Named states keep application protocols explicit. SQLite CHECK constraints
// provide a second boundary; valid transitions still require fenced commands.
type ItemState string

const (
	ItemImporting ItemState = "importing"
	ItemReady     ItemState = "ready"
	ItemDeleting  ItemState = "deleting"
	ItemMissing   ItemState = "missing"
)

type AssetState string

const (
	AssetCandidate AssetState = "candidate"
	AssetVerified  AssetState = "verified"
	AssetPublished AssetState = "published"
	AssetMissing   AssetState = "missing"
)

type SessionState string

const (
	SessionCreating   SessionState = "creating"
	SessionReceiving  SessionState = "receiving"
	SessionCompleting SessionState = "completing"
	SessionQueued     SessionState = "queued"
	SessionProcessing SessionState = "processing"
	SessionFailed     SessionState = "failed"
)

type JobKind string

const (
	JobIngest  JobKind = "ingest"
	JobPoster  JobKind = "poster"
	JobCleanup JobKind = "cleanup"
	JobDelete  JobKind = "delete"
)

type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobRetryWait JobState = "retry_wait"
	JobFailed    JobState = "failed"
)
