package oauth

import (
	"fmt"

	"github.com/pkg/browser"
)

// Opener is the function used to launch the system browser. It is a variable
// so tests can substitute it. Default: github.com/pkg/browser.OpenURL.
var Opener = browser.OpenURL

// Open launches the given URL in the system browser. On failure, callers
// should still print the URL to stderr as a headless fallback.
func Open(url string) error {
	if err := Opener(url); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	return nil
}
