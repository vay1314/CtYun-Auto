package update

type Status string

const (
	StatusIdle        Status = "idle"
	StatusChecking    Status = "checking"
	StatusAvailable   Status = "available"
	StatusDownloading Status = "downloading"
	StatusVerifying   Status = "verifying"
	StatusStaged      Status = "staged"
	StatusStopping    Status = "stopping"
	StatusInstalling  Status = "installing"
	StatusRestarting  Status = "restarting"
	StatusSuccess     Status = "success"
	StatusFailed      Status = "failed"
	StatusRollingBack Status = "rolling_back"
	StatusRolledBack  Status = "rolled_back"
)

type Progress struct {
	Status        Status `json:"status"`
	Percent       int    `json:"percent"`
	Received      int64  `json:"received"`
	Total         int64  `json:"total"`
	Message       string `json:"message"`
	FromVersion   string `json:"fromVersion,omitempty"`
	TargetVersion string `json:"targetVersion,omitempty"`
	UpdatedAt     string `json:"updatedAt,omitempty"`
}
