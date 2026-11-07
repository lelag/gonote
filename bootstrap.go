package gonote

import (
	"encoding/json"
	"regexp"
	"strings"
)

var jsKey = regexp.MustCompile(`([{,]\s*)([a-zA-Z_][a-zA-Z_0-9]*)\s*:`)
var startCall = regexp.MustCompile(`\bStart\s*\(`)

// bootstrap reads the literal passed to Start. It does not evaluate JavaScript.
func bootstrap(html []byte) (node, error) {
	s := string(html)
	start := startCall.FindStringIndex(s)
	if start == nil {
		return node{}, classifyError(s)
	}
	s = s[start[1]:]
	i := strings.IndexByte(s, '{')
	if i < 0 {
		return node{}, ErrProtocol
	}
	s = s[i:]
	depth := 0
	quote := byte(0)
	escaped := false
	end := -1
	for i := 0; i < len(s); i++ {
		b := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
			} else if b == '\\' {
				escaped = true
			} else if b == quote {
				quote = 0
			}
			continue
		}
		if b == '\'' || b == '"' {
			quote = b
			continue
		}
		if b == '{' {
			depth++
		}
		if b == '}' {
			depth--
			if depth == 0 {
				end = i + 1
				break
			}
		}
	}
	if end < 0 {
		return node{}, ErrProtocol
	}
	s = s[:end]
	// Convert quoted strings first, leaving double-quoted JSON strings untouched.
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			start := i
			i++
			for i < len(s) {
				if s[i] == '\\' {
					i += 2
					continue
				}
				if s[i] == '"' {
					break
				}
				i++
			}
			if i >= len(s) {
				return node{}, ErrProtocol
			}
			out.WriteString(s[start : i+1])
			continue
		}
		if s[i] != '\'' {
			out.WriteByte(s[i])
			continue
		}
		var val strings.Builder
		i++
		for i < len(s) && s[i] != '\'' {
			if s[i] == '\\' {
				i++
				if i >= len(s) {
					return node{}, ErrProtocol
				}
				switch s[i] {
				case 'n':
					val.WriteByte('\n')
				case 'r':
					val.WriteByte('\r')
				case 't':
					val.WriteByte('\t')
				case '\\', '\'', '/':
					val.WriteByte(s[i])
				default:
					return node{}, ErrProtocol
				}
			} else {
				val.WriteByte(s[i])
			}
			i++
		}
		if i >= len(s) {
			return node{}, ErrProtocol
		}
		b, _ := json.Marshal(val.String())
		out.Write(b)
	}
	s = jsKey.ReplaceAllString(out.String(), `${1}"${2}":`)
	return parseJSON([]byte(s))
}
