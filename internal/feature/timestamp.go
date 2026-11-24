package feature

import (
	"fmt"
	"time"
)

// relativeTimestamp renders a moment as Discord relative time.
func relativeTimestamp(t time.Time) string {
	return fmt.Sprintf("<t:%d:R>", t.Unix())
}

// fullTimestamp renders a moment with the full date and time.
func fullTimestamp(t time.Time) string {
	return fmt.Sprintf("<t:%d:f>", t.Unix())
}

// shortTimestamp renders a moment as a short clock time.
func shortTimestamp(t time.Time) string {
	return fmt.Sprintf("<t:%d:t>", t.Unix())
}
