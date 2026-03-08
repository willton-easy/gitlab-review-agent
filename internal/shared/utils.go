package shared

import (
	"database/sql"
	"strconv"
	"strings"
)

func StrPtr(s string) *string { return &s }

func DerefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func DerefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func NullTimeToString(t sql.NullTime) *string {
	if !t.Valid {
		return nil
	}
	s := t.Time.Format("2006-01-02T15:04:05Z07:00")
	return &s
}

func PtrCategoryToStr(c *CommentCategory) *string {
	if c == nil {
		return nil
	}
	s := string(*c)
	return &s
}

func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func ParseCSV(s string) []string {
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func DerefCategory(c *CommentCategory) CommentCategory {
	if c == nil {
		return CategoryLogic
	}
	return *c
}

func GetIntOr(input ToolInput, key string, def int) int {
	v, ok := input[key]
	if !ok {
		return def
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i
		}
	}
	return def
}
