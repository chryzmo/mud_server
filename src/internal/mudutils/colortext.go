package mudutils

import (
	"fmt"
)

const (
	COLOR_RED = "31m"
	COLOR_GREEN = "32m"
	COLOR_BLUE = "34m"
	COLOR_MAGENTA = "35m"
)

//\033[1;34m%s\033[0m
func ColorString(message string, colorcode string) string {
	return fmt.Sprintf("\033[1;%s%s\033[0m", colorcode, message)
}

// BoldString wraps a message in ANSI bold without changing its colour.
func BoldString(message string) string {
	return fmt.Sprintf("\033[1m%s\033[0m", message)
}