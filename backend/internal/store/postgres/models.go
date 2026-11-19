package postgres

import (
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("record not found")
	ErrAlreadyVoted = errors.New("user has already voted in this poll")
	ErrPollClosed   = errors.New("poll is closed")
)

type Event struct {
	ID               string     `json:"id"`
	EventNumber      string     `json:"event_number"`
	Title            string     `json:"title"`
	TitleMain        string     `json:"title_main"`
	Subtitle         string     `json:"subtitle"`
	SessionCode      string     `json:"session_code"`
	Description      string     `json:"description"`
	HostName         string     `json:"host_name"`
	HostAvatarLetter string     `json:"host_avatar_letter"`
	StageName        string     `json:"stage_name"`
	StageResolution  string     `json:"stage_resolution"`
	DateEyebrow      string     `json:"date_eyebrow"`
	Status           string     `json:"status"` // scheduled, live, paused, ended
	StartedAt        *time.Time `json:"started_at"`
	EndedAt          *time.Time `json:"ended_at"`
	PausedMs         int64      `json:"paused_ms"`
	PeakAudience     int        `json:"peak_audience"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type Poll struct {
	ID        string     `json:"id"`
	EventID   string     `json:"event_id"`
	Question  string     `json:"question"`
	Status    string     `json:"status"` // active, closed
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at"`
}

type PollOption struct {
	ID          int64     `json:"id"`
	PollID      string    `json:"poll_id"`
	OptionIndex int       `json:"option_index"`
	OptionText  string    `json:"option_text"`
	VoteCount   int       `json:"vote_count"`
	CreatedAt   time.Time `json:"created_at"`
}

type PollWithDetails struct {
	Poll
	Options         []PollOption `json:"options"`
	Votes           []int        `json:"votes"`
	Percentages     []int        `json:"percentages"`
	TotalVotes      int          `json:"total_votes"`
	UserVotedOption *int         `json:"user_voted_option,omitempty"`
}

type Viewer struct {
	ID          string    `json:"id"`
	DisplayName string    `json:"display_name"`
	City        string    `json:"city"`
	Color       string    `json:"color"`
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

type AttendanceSession struct {
	ID       int64      `json:"id"`
	ViewerID string     `json:"viewer_id"`
	EventID  string     `json:"event_id"`
	JoinedAt time.Time  `json:"joined_at"`
	LeftAt   *time.Time `json:"left_at"`
}

type ChatMessage struct {
	ID        int64     `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	UserName  string    `json:"user_name"`
	UserColor string    `json:"user_color"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"created_at"`
}

type ReactionRollup struct {
	ID           int64     `json:"id"`
	EventID      string    `json:"event_id"`
	MinuteBucket time.Time `json:"minute_bucket"`
	Emoji        string    `json:"emoji"`
	Count        int       `json:"count"`
	CreatedAt    time.Time `json:"created_at"`
}

type ReactionTotal struct {
	Emoji string `json:"emoji"`
	Total int64  `json:"total"`
}

type ActivityItem struct {
	ID           int64     `json:"id"`
	EventID      string    `json:"event_id"`
	ActivityType string    `json:"activity_type"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	Icon         string    `json:"icon"`
	Tone         string    `json:"tone"`
	CreatedAt    time.Time `json:"created_at"`
}
