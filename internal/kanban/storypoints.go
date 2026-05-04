package kanban

import (
	"strconv"
	"strings"
)

func FormatStoryPoints(value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "" {
		return "0"
	}
	return text
}
