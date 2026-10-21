package timelog

import (
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	dateLayout = "2006-01-02"
	maxNote    = 500
	// maxSeconds caps one entry at a day.
	maxSeconds = 24 * 3600
)

// Request is the JSON body for writing or editing an entry by hand.
type Request struct {
	// SpentOn is the day, YYYY-MM-DD.
	SpentOn string `json:"spent_on"`
	// Duration is like "1:30", "1h 30m", "45m" or "1.5" (hours).
	Duration string `json:"duration"`
	Note     string `json:"note"`
}

// Validate returns the entry and per-field errors.
func (r Request) Validate() (Params, map[string]string) {
	fields := map[string]string{}
	p := Params{Note: strings.TrimSpace(r.Note)}
	d, err := time.Parse(dateLayout, strings.TrimSpace(r.SpentOn))
	if err != nil {
		fields["spent_on"] = "Enter a valid date (YYYY-MM-DD)"
	}
	p.SpentOn = d
	secs, ok := ParseDuration(r.Duration)
	switch {
	case !ok:
		fields["duration"] = "Enter time like 1:30, 1h 30m or 45m"
	case secs == 0:
		fields["duration"] = "Enter how long it took"
	case secs > maxSeconds:
		fields["duration"] = "One entry is at most 24 hours"
	}
	p.Seconds = secs
	if utf8.RuneCountInString(p.Note) > maxNote {
		fields["note"] = "The note is too long"
	}
	return p, fields
}

// ParseDuration reads a duration a person types into seconds: "1:30" (hours
// and minutes), "1h 30m", "2h", "45m", "45 хв", or a bare number of hours
// ("1.5", "1,5"). Whole minutes only.
func ParseDuration(s string) (int64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, false
	}
	if h, m, ok := strings.Cut(s, ":"); ok {
		hours, err1 := strconv.Atoi(strings.TrimSpace(h))
		mins, err2 := strconv.Atoi(strings.TrimSpace(m))
		if err1 != nil || err2 != nil || hours < 0 || mins < 0 || mins > 59 {
			return 0, false
		}
		return int64(hours*3600 + mins*60), true
	}
	if n, err := strconv.ParseFloat(strings.ReplaceAll(s, ",", "."), 64); err == nil {
		if !(n >= 0 && n <= 1000) { // also NaN

			return 0, false
		}
		return int64(n*60+0.5) * 60, true
	}
	// "1h 30m", "1 год 30 хв": a number then a unit, as many times as given.
	var total int64
	units := map[string]int64{"h": 3600, "hr": 3600, "hrs": 3600, "hour": 3600, "hours": 3600, "г": 3600, "год": 3600,
		"m": 60, "min": 60, "mins": 60, "minute": 60, "minutes": 60, "хв": 60}
	rest := strings.TrimSpace(s)
	for rest != "" {
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 {
			return 0, false
		}
		n, _ := strconv.ParseInt(rest[:i], 10, 64)
		rest = strings.TrimLeft(rest[i:], " ")
		j := 0
		for j < len(rest) && rest[j] != ' ' && (rest[j] < '0' || rest[j] > '9') {
			j++
		}
		unit, ok := units[rest[:j]]
		if !ok || n > 100000 {
			return 0, false
		}
		total += n * unit
		rest = strings.TrimLeft(rest[j:], " ")
	}
	return total, true
}

// UserDTO is who wrote an entry.
type UserDTO struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// DTO is a time entry as a side of its task sees it.
type DTO struct {
	ID        string  `json:"id"`
	TaskID    string  `json:"task_id"`
	TaskTitle string  `json:"task_title"`
	User      UserDTO `json:"user"`
	SpentOn   string  `json:"spent_on"`
	// Seconds is the time so far; while RunningSince is set the timer adds
	// the time since then.
	Seconds      int64      `json:"seconds"`
	Note         string     `json:"note"`
	RunningSince *time.Time `json:"running_since"`
	// CanEdit is true for the author, who may change and delete it.
	CanEdit   bool      `json:"can_edit"`
	CreatedAt time.Time `json:"created_at"`
}

// ToDTO maps an entry for the viewer.
func ToDTO(e Entry, viewerID string) DTO {
	return DTO{
		ID:           e.ID,
		TaskID:       e.TaskID,
		TaskTitle:    e.TaskTitle,
		User:         UserDTO(e.User),
		SpentOn:      e.SpentOn.Format(dateLayout),
		Seconds:      e.Seconds,
		Note:         e.Note,
		RunningSince: e.RunningSince,
		CanEdit:      e.User.ID == viewerID,
		CreatedAt:    e.CreatedAt,
	}
}
