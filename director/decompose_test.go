package director

import (
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestSplitSentences(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"Hello world.", []string{"Hello world."}},
		{"First. Second! Third?", []string{"First.", "Second!", "Third?"}},
		{"No terminal punctuation", []string{"No terminal punctuation"}},
		{"Trailing. ", []string{"Trailing."}},
	}
	for _, tc := range cases {
		got := splitSentences(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitSentences(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitSentences(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}

func TestMentionsAvatar(t *testing.T) {
	positive := []string{
		"Show my avatar here.",
		"Switch to the webcam.",
		"I will narrate this part.",
		"Let the narrator explain.",
		"The presenter walks through it.",
		"Let me show you.",
		"Here is my face on screen.",
	}
	for _, s := range positive {
		if !mentionsAvatar(s) {
			t.Errorf("mentionsAvatar(%q) = false, want true", s)
		}
	}

	negative := []string{
		"This is a demo of the feature.",
		"The code is framed nicely.",
		"Just plain narration-free text about functions.",
		"",
	}
	for _, s := range negative {
		if mentionsAvatar(s) {
			t.Errorf("mentionsAvatar(%q) = true, want false", s)
		}
	}
}

func TestInferCategory(t *testing.T) {
	cases := []struct {
		text string
		want domain.ShotCategory
	}{
		{"Let's open the terminal and run a command.", domain.ShotCategoryTerminal},
		{"Here is a function and a variable.", domain.ShotCategoryCode},
		{"Let me introduce myself as your presenter.", domain.ShotCategoryAvatar},
		{"This is just a plain sentence about the UI.", domain.ShotCategoryUI},
	}
	for _, tc := range cases {
		got := inferCategory(tc.text)
		if got != tc.want {
			t.Errorf("inferCategory(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
}

func TestEstimateDuration(t *testing.T) {
	short := estimateDuration("Hi.")
	if short != minShotDuration {
		t.Fatalf("estimateDuration(short) = %v, want the floor %v", short, minShotDuration)
	}

	longText := "one two three four five six seven eight nine ten eleven twelve"
	long := estimateDuration(longText)
	if long <= minShotDuration {
		t.Fatalf("estimateDuration(long) = %v, want more than the floor %v", long, minShotDuration)
	}
	// 12 words at 2.5 words/sec = 4.8s.
	want := time.Duration(float64(12) / wordsPerSecond * float64(time.Second))
	if long != want {
		t.Fatalf("estimateDuration(12 words) = %v, want %v", long, want)
	}
}

func TestEstimateDurationIsDeterministic(t *testing.T) {
	text := "A sentence with a fixed number of words in it."
	a := estimateDuration(text)
	b := estimateDuration(text)
	if a != b {
		t.Fatalf("estimateDuration is not deterministic: %v vs %v", a, b)
	}
}
