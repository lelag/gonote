// Package demo provides synthetic school data. It never makes network calls.
package demo

import (
	"time"

	"github.com/lelag/gonote"
)

type Data struct {
	Profile   gonote.Profile
	Homework  []gonote.Assignment
	Lessons   []gonote.Lesson
	Timetable []gonote.TimetableEntry
	Grades    gonote.GradeReport
}

func New(now time.Time) Data {
	loc := now.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	monday := today.AddDate(0, 0, -(int(today.Weekday())+6)%7)
	next := today
	if next.Weekday() == time.Saturday {
		next = next.AddDate(0, 0, 2)
	}
	if next.Weekday() == time.Sunday {
		next = next.AddDate(0, 0, 1)
	}
	math := gonote.Resource{ID: "maths", Name: "Mathématiques"}
	french := gonote.Resource{ID: "francais", Name: "Français"}
	english := gonote.Resource{ID: "anglais", Name: "Anglais"}
	science := gonote.Resource{ID: "physique", Name: "Physique-chimie"}
	period := gonote.Period{Resource: gonote.Resource{ID: "t1", Name: "Trimestre 1", Kind: 2}, Start: today.AddDate(0, -2, 0), End: today.AddDate(0, 1, 0)}
	d := Data{Profile: gonote.Profile{Student: gonote.Resource{ID: "demo-alice", Name: "Alice Martin", Kind: 4}, School: "Collège des Amandiers", Class: "4e B", Periods: []gonote.Period{period}, DefaultGradePeriod: "t1", FirstMonday: monday.AddDate(0, 0, -56)}}
	d.Homework = []gonote.Assignment{
		{ID: "h-maths", Subject: math, Description: "<p>Faire les exercices <b>12 à 15</b>, page 84.</p><p>Justifier chaque étape du calcul et apporter la calculatrice.</p>", Given: today.AddDate(0, 0, -2), Due: next, DurationMinutes: 20, Submission: 1, LessonID: "l-maths", Attachments: []gonote.Attachment{}},
		{ID: "h-anglais", Subject: english, Description: "<p>Préparer une présentation de <b>mon animal préféré</b> en anglais.</p><ul><li>Écrire cinq phrases.</li><li>S’entraîner à les lire à voix haute.</li></ul>", Given: today.AddDate(0, 0, -1), Due: next, DurationMinutes: 15, Submission: 2, CanSubmit: true, Attachments: []gonote.Attachment{{ID: "a-english", Name: "My favourite animal.pdf", Kind: 1}}},
		{ID: "h-francais", Subject: french, Description: "Relire le chapitre 3 du <i>Petit Prince</i> et relever trois comparaisons.", Given: today.AddDate(0, 0, -3), Due: next.AddDate(0, 0, 1), Done: true, DurationMinutes: 15, Submission: 1, Attachments: []gonote.Attachment{}},
		{ID: "h-physique", Subject: science, Description: "Réviser les états de la matière pour le contrôle. Refaire le schéma de l’expérience.", Given: today.AddDate(0, 0, -1), Due: next.AddDate(0, 0, 3), DurationMinutes: 25, Attachments: []gonote.Attachment{}},
	}
	d.Lessons = []gonote.Lesson{{ID: "l-maths", Subject: math, Start: today.Add(8 * time.Hour), End: today.Add(9 * time.Hour), Teachers: []string{"Mme Laurent"}, Contents: []gonote.LessonContent{{Title: "Les fractions", Category: "À retenir", Description: "<p>Additionner deux fractions de même dénominateur.</p><p>Exemple : 2/7 + 3/7 = 5/7.</p>", Attachments: []gonote.Attachment{{ID: "a-maths", Name: "Fiche méthode — fractions.pdf", Kind: 1}}}}}, {ID: "l-francais", Subject: french, Start: today.Add(10 * time.Hour), End: today.Add(11 * time.Hour), Teachers: []string{"M. Bernard"}, Contents: []gonote.LessonContent{{Title: "Le récit poétique", Description: "Lecture et échange autour du Petit Prince. Repérer une comparaison et une métaphore.", Attachments: []gonote.Attachment{}}}}}
	for i := 0; i < 5; i++ {
		day := monday.AddDate(0, 0, i)
		for j, s := range []gonote.Resource{math, french, english, science} {
			if i == 2 && j > 1 {
				continue
			}
			start := day.Add(time.Duration(8+j*2) * time.Hour)
			e := gonote.TimetableEntry{ID: day.Format("20060102") + s.ID, Subject: s, Start: start, End: start.Add(time.Hour), Kind: "lesson", Rooms: []string{[]string{"B204", "A108", "C102", "Labo 2"}[j]}, Teachers: []string{[]string{"Mme Laurent", "M. Bernard", "Ms Wilson", "M. Moreau"}[j]}, Groups: []string{}}
			if i == 1 && j == 2 {
				e.Status = "Changement de salle"
				e.Rooms = []string{"A203"}
			}
			if i == 4 && j == 3 {
				e.Canceled = true
				e.Status = "Cours annulé"
			}
			d.Timetable = append(d.Timetable, e)
		}
	}
	value := func(v float64) gonote.GradeValue { return gonote.GradeValue{Points: &v} }
	avg := value(14.8)
	class := value(12.6)
	d.Grades = gonote.GradeReport{Period: period, Average: &avg, ClassAverage: &class, OutOf: value(20), Subjects: []gonote.SubjectAverage{{Subject: math, Student: &avg, Class: &class, OutOf: value(20)}}, Grades: []gonote.Grade{
		{ID: "n-physique", Subject: science, Date: today.AddDate(0, 0, -1), Value: value(16), OutOf: value(20), Coefficient: 1, Comment: "Les états de la matière", Feedback: "Très bon travail, continue ainsi !", Attachments: []gonote.Attachment{}},
		{ID: "n-maths", Subject: math, Date: today.AddDate(0, 0, -2), Value: value(8.5), OutOf: value(10), Coefficient: 2, Comment: "Calcul avec les fractions", Attachments: []gonote.Attachment{}},
		{ID: "n-francais", Subject: french, Date: today.AddDate(0, 0, -4), Value: value(13), OutOf: value(20), Coefficient: 1, Comment: "Expression écrite", Attachments: []gonote.Attachment{}},
		{ID: "n-anglais", Subject: english, Date: today.AddDate(0, 0, -5), Value: gonote.GradeValue{Status: 1}, OutOf: value(20), Coefficient: 1, Comment: "Compréhension orale", Attachments: []gonote.Attachment{}},
	}}
	for i := range d.Grades.Grades {
		d.Grades.Grades[i].PeriodID = period.ID
	}
	return d
}
