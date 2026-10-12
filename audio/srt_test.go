package audio

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestSRTFormatParseRoundTrip(t *testing.T) {
	asset := testAsset()
	encoded, err := FormatSRT(asset, testCues())
	if err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:00,000 --> 00:00:01,000\nhello\n\n2\n00:00:01,000 --> 00:00:02,000\nworld\n\n"
	if string(encoded) != want {
		t.Fatalf("format = %q", encoded)
	}
	parsed, err := ParseSRT(bytes.ReplaceAll(encoded, []byte("\n"), []byte("\r\n")), asset)
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed) != 2 || parsed[0].ID != "srt-000001" || parsed[1].End != testCues()[1].End {
		t.Fatalf("parse = %+v", parsed)
	}
}

func TestSRTRejectsUnboundedOrNonCanonicalInput(t *testing.T) {
	asset := testAsset()
	for _, input := range [][]byte{[]byte("01\n00:00:00,000 --> 00:00:01,000\ntext\n\n"), []byte("+1\n00:00:00,000 --> 00:00:01,000\ntext\n\n"), []byte("1\n000:00:00,000 --> 000:00:01,000\ntext\n\n"), []byte("1\n00:00:00,000 --> 00:00:03,000\ntext\n\n"), []byte("1\n00:00:00,000 --> 00:00:01,000\ntext\n")} {
		if _, err := ParseSRT(input, asset); !errors.Is(err, ErrInvalid) {
			t.Fatalf("accepted %q: %v", input, err)
		}
	}
}

func TestSRTFormatRejectsSubMillisecondCue(t *testing.T) {
	cues := testCues()
	cues[0].End = time.Second + time.Nanosecond
	cues[1].Start = cues[0].End
	if _, err := FormatSRT(testAsset(), cues); !errors.Is(err, ErrInvalid) {
		t.Fatalf("formatted sub-millisecond cue: %v", err)
	}
}

func TestSRTAcceptsStandardTwoDigitExternalLiteralAndMultibyteRoundTrip(t *testing.T) {
	asset := testAsset()
	external := []byte("1\n00:00:00,000 --> 00:00:01,000\nhello\n\n2\n00:00:01,000 --> 00:00:02,000\n世界 👋\n\n")
	parsed, err := ParseSRT(external, asset)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed[1].Text; got != "世界 👋" {
		t.Fatalf("multibyte text = %q", got)
	}
	formatted, err := FormatSRT(asset, parsed)
	if err != nil || !bytes.Equal(formatted, external) {
		t.Fatalf("round trip = %q, %v", formatted, err)
	}
}

func TestSRTRejectsInvalidUTF8(t *testing.T) {
	asset := testAsset()
	badInput := []byte("1\n00:00:00,000 --> 00:00:01,000\n")
	badInput = append(badInput, 0xff, '\n', '\n')
	if _, err := ParseSRT(badInput, asset); !errors.Is(err, ErrInvalid) {
		t.Fatalf("parsed invalid UTF-8: %v", err)
	}
	cues := testCues()
	cues[0].Text = string([]byte{0xff})
	if _, err := FormatSRT(asset, cues); !errors.Is(err, ErrInvalid) {
		t.Fatalf("formatted invalid UTF-8: %v", err)
	}
}

func TestSRTRejectsHoursBeyondTwoDigitRange(t *testing.T) {
	asset := testAsset()
	asset.Duration = 101 * time.Hour
	if _, err := ParseSRT([]byte("1\n100:00:00,000 --> 100:00:01,000\ntext\n\n"), asset); !errors.Is(err, ErrInvalid) {
		t.Fatalf("parsed three-digit hour field: %v", err)
	}
	cues := []domain.SubtitleCue{{ID: "cue-1", AudioAssetID: asset.ID, Text: "text", Start: 99 * time.Hour, End: 100 * time.Hour}}
	if _, err := FormatSRT(asset, cues); !errors.Is(err, ErrInvalid) {
		t.Fatalf("formatted hour beyond two-digit range: %v", err)
	}
}
