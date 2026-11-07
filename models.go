// Package gonote provides a client for the unofficial PRONOTE web
// protocol. It has no terminal dependencies and can be used by a CLI or TUI.
package gonote

import "time"

type Resource struct {
	ID   string `json:"id"`
	Kind int    `json:"kind"`
	Name string `json:"name"`
}

type Period struct {
	Resource
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type Profile struct {
	Student            Resource  `json:"student"`
	School             string    `json:"school"`
	Class              string    `json:"class"`
	Periods            []Period  `json:"periods"`
	DefaultGradePeriod string    `json:"default_grade_period"`
	FirstMonday        time.Time `json:"first_monday"`
}

// DateRange is inclusive of both calendar dates in the client's school timezone.
type DateRange struct{ From, To time.Time }

type Attachment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     int    `json:"kind"`
	URL      string `json:"url,omitempty"`
	Category string `json:"category,omitempty"`
}

type Assignment struct {
	ID              string       `json:"id"`
	Subject         Resource     `json:"subject"`
	Description     string       `json:"description"`
	Given           time.Time    `json:"given"`
	Due             time.Time    `json:"due"`
	Done            bool         `json:"done"`
	DurationMinutes int          `json:"duration_minutes"`
	Submission      int          `json:"submission"`
	CanSubmit       bool         `json:"can_submit"`
	LessonID        string       `json:"lesson_id,omitempty"`
	Attachments     []Attachment `json:"attachments"`
	Submitted       *Attachment  `json:"submitted,omitempty"`
}

type LessonContent struct {
	Title       string       `json:"title"`
	Category    string       `json:"category"`
	Description string       `json:"description"`
	Attachments []Attachment `json:"attachments"`
}

type Lesson struct {
	ID       string          `json:"id"`
	Subject  Resource        `json:"subject"`
	Start    time.Time       `json:"start"`
	End      time.Time       `json:"end"`
	Teachers []string        `json:"teachers"`
	Contents []LessonContent `json:"contents"`
}

type TimetableEntry struct {
	ID       string    `json:"id"`
	Subject  Resource  `json:"subject"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Teachers []string  `json:"teachers"`
	Rooms    []string  `json:"rooms"`
	Groups   []string  `json:"groups"`
	Status   string    `json:"status,omitempty"`
	Canceled bool      `json:"canceled"`
	Kind     string    `json:"kind"`
	LessonID string    `json:"lesson_id,omitempty"`
}

// GradeValue keeps special statuses separate from numeric values. Points is nil
// for absent, exempt, ungraded and unknown values; zero remains a real zero.
type GradeValue struct {
	Points *float64 `json:"points"`
	Status int      `json:"status"`
	Raw    string   `json:"raw,omitempty"`
}

type Grade struct {
	PeriodID    string       `json:"period_id,omitempty"`
	ID          string       `json:"id"`
	Subject     Resource     `json:"subject"`
	Date        time.Time    `json:"date"`
	Value       GradeValue   `json:"value"`
	OutOf       GradeValue   `json:"out_of"`
	Coefficient float64      `json:"coefficient"`
	Comment     string       `json:"comment"`
	Feedback    string       `json:"feedback,omitempty"`
	Average     *GradeValue  `json:"average,omitempty"`
	Bonus       bool         `json:"bonus"`
	Optional    bool         `json:"optional"`
	Attachments []Attachment `json:"attachments"`
}

type SubjectAverage struct {
	Subject Resource    `json:"subject"`
	Student *GradeValue `json:"student,omitempty"`
	Class   *GradeValue `json:"class,omitempty"`
	OutOf   GradeValue  `json:"out_of"`
}

type GradeReport struct {
	Period       Period           `json:"period"`
	Grades       []Grade          `json:"grades"`
	Subjects     []SubjectAverage `json:"subjects"`
	Average      *GradeValue      `json:"average,omitempty"`
	ClassAverage *GradeValue      `json:"class_average,omitempty"`
	OutOf        GradeValue       `json:"out_of"`
}
