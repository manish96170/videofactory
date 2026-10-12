package audio

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
)

const (
	MaxSRTBytes   = 1 << 20
	MaxSRTCueText = 4096
	MaxSRTHours   = 99
)

// ParseSRT parses bounded, canonical SRT text into cues linked to asset. Input
// must use one positive sequential cue number, a canonical time range, and one
// or more nonblank text lines per cue. Blank separator lines are required.
func ParseSRT(raw []byte, asset domain.AudioAsset) ([]domain.SubtitleCue, error) {
	if ValidateAudioAsset(asset) != nil || len(raw) == 0 || len(raw) > MaxSRTBytes || !utf8.Valid(raw) {
		return nil, ErrInvalid
	}
	if bytes.Contains(raw, []byte("\r")) {
		raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		if bytes.Contains(raw, []byte("\r")) {
			return nil, ErrInvalid
		}
	}
	if !bytes.HasSuffix(raw, []byte("\n\n")) {
		return nil, ErrInvalid
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 1024), MaxSRTCueText+1)
	var cues []domain.SubtitleCue
	for expected := 1; scanner.Scan(); expected++ {
		if len(cues) == MaxCuesPerResult {
			return nil, ErrInvalid
		}
		sequenceLine := scanner.Text()
		sequence, err := strconv.Atoi(sequenceLine)
		if err != nil || sequence != expected || sequenceLine != strconv.Itoa(expected) {
			return nil, ErrInvalid
		}
		if !scanner.Scan() {
			return nil, ErrInvalid
		}
		parts := strings.Split(scanner.Text(), " --> ")
		if len(parts) != 2 {
			return nil, ErrInvalid
		}
		start, err := parseSRTTime(parts[0])
		if err != nil {
			return nil, ErrInvalid
		}
		end, err := parseSRTTime(parts[1])
		if err != nil {
			return nil, ErrInvalid
		}
		var text []string
		textSize := 0
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				break
			}
			textSize += len(line)
			if len(text) > 0 {
				textSize++ // newline inserted by Join below
			}
			if textSize > MaxSRTCueText {
				return nil, ErrInvalid
			}
			text = append(text, line)
		}
		if len(text) == 0 {
			return nil, ErrInvalid
		}
		joined := strings.Join(text, "\n")
		if len(joined) > MaxSRTCueText || strings.TrimSpace(joined) == "" {
			return nil, ErrInvalid
		}
		cues = append(cues, domain.SubtitleCue{
			ID:           domain.ID(fmt.Sprintf("srt-%06d", sequence)),
			AudioAssetID: asset.ID,
			Text:         joined,
			Start:        start,
			End:          end,
		})
	}
	if scanner.Err() != nil || len(cues) == 0 || ValidateSubtitleCues(asset, cues) != nil {
		return nil, ErrInvalid
	}
	return append([]domain.SubtitleCue(nil), cues...), nil
}

// FormatSRT serializes already-valid cues in a stable, canonical SRT form.
func FormatSRT(asset domain.AudioAsset, cues []domain.SubtitleCue) ([]byte, error) {
	if len(cues) == 0 || ValidateSubtitleCues(asset, cues) != nil {
		return nil, ErrInvalid
	}
	var out bytes.Buffer
	for i, cue := range cues {
		if cue.Start%time.Millisecond != 0 || cue.End%time.Millisecond != 0 || cue.Start/time.Hour > MaxSRTHours || cue.End/time.Hour > MaxSRTHours || !utf8.ValidString(cue.Text) || len(cue.Text) > MaxSRTCueText || strings.Contains(cue.Text, "\r") || strings.HasPrefix(cue.Text, "\n") || strings.HasSuffix(cue.Text, "\n") || strings.Contains(cue.Text, "\n\n") {
			return nil, ErrInvalid
		}
		fmt.Fprintf(&out, "%d\n%s --> %s\n%s\n\n", i+1, formatSRTTime(cue.Start), formatSRTTime(cue.End), cue.Text)
		if out.Len() > MaxSRTBytes {
			return nil, ErrInvalid
		}
	}
	return append([]byte(nil), out.Bytes()...), nil
}

func parseSRTTime(value string) (time.Duration, error) {
	if len(value) != len("00:00:00,000") || value[2] != ':' || value[5] != ':' || value[8] != ',' {
		return 0, ErrInvalid
	}
	for _, position := range []int{0, 1, 3, 4, 6, 7, 9, 10, 11} {
		if value[position] < '0' || value[position] > '9' {
			return 0, ErrInvalid
		}
	}
	hours, err1 := strconv.Atoi(value[0:2])
	minutes, err2 := strconv.Atoi(value[3:5])
	seconds, err3 := strconv.Atoi(value[6:8])
	milliseconds, err4 := strconv.Atoi(value[9:12])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || hours > MaxSRTHours || minutes > 59 || seconds > 59 || milliseconds > 999 {
		return 0, ErrInvalid
	}
	return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second + time.Duration(milliseconds)*time.Millisecond, nil
}

func formatSRTTime(value time.Duration) string {
	hours := value / time.Hour
	value %= time.Hour
	minutes := value / time.Minute
	value %= time.Minute
	seconds := value / time.Second
	milliseconds := (value % time.Second) / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d,%03d", hours, minutes, seconds, milliseconds)
}
