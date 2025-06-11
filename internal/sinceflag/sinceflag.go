package sinceflag

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// SinceValue stores parsed time and the original string
type SinceValue struct {
	Time  time.Time
	input string
}

// String returns the original input string.
func (s *SinceValue) String() string {
	return s.input
}

// Set parses the input string and sets the Time field.
// It supports various date formats and extended durations like "1y", "2mo", etc.
// If parsing fails, it returns an error.
func (s *SinceValue) Set(input string) error {
	t, err := ParseSince(input)
	if err != nil {
		return fmt.Errorf("invalid --since value: %v", err)
	}

	s.Time = t
	s.input = input

	return nil
}

// Type returns the flag name.
func (s *SinceValue) Type() string {
	return "since"
}

// durationRE matches durations like "1y", "2mo", "3w", "4d", "5h", "6m", "7s".
var durationRE = regexp.MustCompile(`(?i)(\d+)(y|mo|w|d|h|m|s)`)

// ParseSince parses a string into a time.Time value.
func ParseSince(input string) (time.Time, error) {
	now := time.Now()

	// Try known date formats.
	formats := []string{
		"2006-01-02",
		"2006-01-02 15:04:05",
		time.RFC3339,
	}
	for _, f := range formats {
		if t, err := time.Parse(f, input); err == nil {
			return t, nil
		}
	}

	// Parse as extended duration.
	matches := durationRE.FindAllStringSubmatch(strings.ToLower(input), -1)
	if len(matches) == 0 {
		return time.Time{}, errors.New("unrecognized time or duration format")
	}

	var lessThanDayUnits bool

	var total time.Duration
	for _, m := range matches {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("invalid number in duration: %s", m[1])
		}
		unit := m[2]

		switch unit {
		case "y":
			total += time.Hour * 24 * 365 * time.Duration(n)
		case "mo":
			total += time.Hour * 24 * 30 * time.Duration(n)
		case "w":
			total += time.Hour * 24 * 7 * time.Duration(n)
		case "d":
			total += time.Hour * 24 * time.Duration(n)
		case "h":
			total += time.Hour * time.Duration(n)
			lessThanDayUnits = true
		case "m":
			total += time.Minute * time.Duration(n)
			lessThanDayUnits = true
		case "s":
			total += time.Second * time.Duration(n)
			lessThanDayUnits = true
		default:
			return time.Time{}, fmt.Errorf("unknown unit: %s", unit)
		}
	}

	since := now.Add(-total)

	// if we get less than a day, we trump the actual duration to midnight of the computed time.
	if !lessThanDayUnits {
		since = time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
	}

	return since, nil
}
