package gonote

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// node hides wire wrappers and case differences without discarding original
// field names or converting grade strings into floats prematurely.
type node struct{ v any }

func unwrap(v any) any {
	for {
		m, ok := v.(map[string]any)
		if !ok {
			return v
		}
		if _, tagged := m["_T"]; !tagged {
			return v
		}
		inner, ok := m["V"]
		if !ok {
			return v
		}
		v = inner
	}
}
func (n node) get(keys ...string) node {
	m, _ := unwrap(n.v).(map[string]any)
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return node{v}
		}
		for k, v := range m {
			if strings.EqualFold(k, key) {
				return node{v}
			}
		}
	}
	return node{}
}
func (n node) has(key string) bool { return n.get(key).v != nil }
func (n node) list() []node {
	a, _ := unwrap(n.v).([]any)
	out := make([]node, 0, len(a))
	for _, v := range a {
		out = append(out, node{v})
	}
	return out
}
func (n node) text() string {
	switch v := unwrap(n.v).(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}
func (n node) number() float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(n.text(), ",", "."), 64)
	return f
}
func (n node) integer() int  { return int(n.number()) }
func (n node) boolean() bool { v, _ := unwrap(n.v).(bool); return v }
func (n node) truthy() bool {
	switch v := unwrap(n.v).(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case json.Number:
		return v != "0"
	case float64:
		return v != 0
	}
	return true
}
func (n node) resource() Resource {
	return Resource{ID: n.get("N").text(), Kind: n.get("G").integer(), Name: n.get("L").text()}
}
func parseJSON(b []byte) (node, error) {
	var v any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return node{}, ErrProtocol
	}
	return node{v}, nil
}

func (n node) date(loc *time.Location) (time.Time, error) {
	s := n.text()
	for _, layout := range []string{"2/1/2006 15:4:5", "2/1/2006 15:4", "2/1/2006", "2/1/06 15h04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(loc), nil
	}
	return time.Time{}, fmt.Errorf("%w : date invalide", ErrProtocol)
}
func wireDate(t time.Time) any                          { return map[string]any{"_T": 7, "V": t.Format("2/1/2006 15:4:5")} }
func optionalDate(n node, loc *time.Location) time.Time { t, _ := n.date(loc); return t }
func gradeValue(n node) GradeValue {
	s := n.text()
	out := GradeValue{Raw: s, Status: 0}
	if s == "" {
		out.Status = -1
		return out
	}
	parts := strings.Split(s, "|")
	if len(parts) > 1 {
		status, err := strconv.Atoi(parts[1])
		if err != nil {
			out.Status = -1
			return out
		}
		out.Status = status
	}
	if out.Status != 0 {
		return out
	}
	f, err := strconv.ParseFloat(strings.ReplaceAll(parts[0], ",", "."), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		out.Status = -1
		return out
	}
	out.Points = &f
	return out
}
func optionalGrade(n node) *GradeValue {
	if n.v == nil || n.text() == "" {
		return nil
	}
	v := gradeValue(n)
	return &v
}
func ints(n node) []int {
	if a := n.list(); len(a) > 0 {
		out := []int{}
		for _, x := range a {
			out = append(out, x.integer())
		}
		return out
	}
	s := strings.Trim(n.text(), "[] ")
	if s == "" {
		return nil
	}
	var out []int
	for _, part := range strings.Split(s, ",") {
		r := strings.Split(strings.TrimSpace(part), "..")
		first, err := strconv.Atoi(r[0])
		if err != nil {
			continue
		}
		last := first
		if len(r) == 2 {
			last, _ = strconv.Atoi(r[1])
		}
		if last-first > 10000 {
			continue
		}
		for i := first; i <= last; i++ {
			out = append(out, i)
		}
	}
	return out
}
