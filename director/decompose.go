// Deterministic planning heuristics: no AI, no network calls, no
// learned judgment — just fixed rules, so FixtureDirector works
// correctly offline and produces the same structure for the same
// prompt every time (PHASES.md Phase 05 "deterministic fixture
// Director").
package director

import (
	"regexp"
	"strings"
	"time"

	"github.com/manish96170/videofactory/domain"
)

// splitSentences breaks text into trimmed, non-empty sentences on
// '.', '!', '?'. Deliberately simple: no NLP library, no sentence
// boundary disambiguation beyond these three punctuation marks.
func splitSentences(text string) []string {
	var sentences []string
	var cur strings.Builder
	for _, r := range text {
		cur.WriteRune(r)
		if r == '.' || r == '!' || r == '?' {
			if s := strings.TrimSpace(cur.String()); s != "" {
				sentences = append(sentences, s)
			}
			cur.Reset()
		}
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		sentences = append(sentences, s)
	}
	return sentences
}

// avatarPattern triggers the AI-capable DeveloperAvatar path instead
// of the deterministic Background+Text path, matching any of its
// keywords as whole words/phrases (case-insensitive) — \b word
// boundaries, so "me" doesn't match inside "demo" or "framed".
var avatarPattern = regexp.MustCompile(`(?i)\b(avatar|webcam|my face|narrate|narrator|presenter|me)\b`)

// mentionsAvatar reports whether text contains any avatar-related
// keyword as a whole word/phrase.
func mentionsAvatar(text string) bool {
	return avatarPattern.MatchString(text)
}

var terminalKeywords = []string{"terminal", "command", "shell", "console"}
var codeKeywords = []string{"code", "function", "variable", "class", "method"}

// inferCategory classifies a shot's text using fixed keyword lists —
// the same "deterministic first" principle as the rest of this
// package (PHASES.md section 4.3).
func inferCategory(text string) domain.ShotCategory {
	lower := strings.ToLower(text)
	switch {
	case mentionsAvatar(text):
		return domain.ShotCategoryAvatar
	case containsAny(lower, terminalKeywords):
		return domain.ShotCategoryTerminal
	case containsAny(lower, codeKeywords):
		return domain.ShotCategoryCode
	default:
		return domain.ShotCategoryUI
	}
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

const (
	// minShotDuration is a floor so a very short sentence still
	// produces a watchable shot, consistent with PHASES.md section
	// 4.4's "~2-3 seconds" small-shot guidance (a floor, not a cap:
	// nothing here enforces an upper bound).
	minShotDuration = 1500 * time.Millisecond
	// wordsPerSecond is a deliberately simple, fixed reading-pace
	// heuristic — not a measured/learned value.
	wordsPerSecond = 2.5
)

// estimateDuration gives a deterministic shot duration from its text,
// using the fixed reading-pace heuristic above rather than any AI
// estimate.
func estimateDuration(text string) time.Duration {
	words := len(strings.Fields(text))
	d := time.Duration(float64(words) / wordsPerSecond * float64(time.Second))
	if d < minShotDuration {
		return minShotDuration
	}
	return d
}
