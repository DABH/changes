package changesapp

import (
	"fmt"
	"time"

	"github.com/shurcooL/issues"
)

// timelineItem represents a timeline item for display purposes.
type timelineItem struct {
	// TimelineItem can be one of issues.Comment, issues.Event.
	TimelineItem interface{}
}

func (i timelineItem) TemplateName() string {
	switch i.TimelineItem.(type) {
	case issues.Comment:
		return "comment"
	case issues.Event:
		return "event"
	default:
		panic(fmt.Errorf("unknown item type %T", i.TimelineItem))
	}
}

func (i timelineItem) CreatedAt() time.Time {
	switch i := i.TimelineItem.(type) {
	case issues.Comment:
		return i.CreatedAt
	case issues.Event:
		return i.CreatedAt
	default:
		panic(fmt.Errorf("unknown item type %T", i))
	}
}

func (i timelineItem) ID() uint64 {
	switch i := i.TimelineItem.(type) {
	case issues.Comment:
		return i.ID
	case issues.Event:
		return i.ID
	default:
		panic(fmt.Errorf("unknown item type %T", i))
	}
}

// byCreatedAtID implements sort.Interface.
type byCreatedAtID []timelineItem

func (s byCreatedAtID) Len() int { return len(s) }
func (s byCreatedAtID) Less(i, j int) bool {
	if s[i].CreatedAt().Equal(s[j].CreatedAt()) {
		// If CreatedAt time is equal, fall back to ID as a tiebreaker.
		return s[i].ID() < s[j].ID()
	}
	return s[i].CreatedAt().Before(s[j].CreatedAt())
}
func (s byCreatedAtID) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
