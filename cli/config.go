package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/manish96170/videofactory/tui"
)

// MaxConfigBytes bounds configuration reads and decoding allocations.
const MaxConfigBytes = 64 << 10

var (
	ErrConfig     = errors.New("cli: invalid configuration; expected version 1 and optional tab (chat, plan, shots, timeline, assets, models, logs)")
	ErrConfigRead = errors.New("cli: cannot read configuration; provide an explicit regular JSON file")
)

// Config is presentation configuration only. Version is required; Tab is
// optional and, when empty, preserves the injected snapshot's focused tab.
// Credentials, provider settings, and execution settings are not accepted.
type Config struct {
	Version int    `json:"version"`
	Tab     string `json:"tab,omitempty"`
}

func (c Config) Validate() error {
	if c.Version != 1 {
		return ErrConfig
	}
	if c.Tab != "" {
		if _, ok := configTab(c.Tab); !ok {
			return ErrConfig
		}
	}
	return nil
}

// ParseConfig accepts exactly one UTF-8 JSON object. Keys are case-sensitive;
// unknown/duplicate keys, nulls, and trailing values are rejected. Diagnostics
// never echo configuration contents.
func ParseConfig(data []byte) (Config, error) {
	if len(data) > MaxConfigBytes || !utf8.Valid(data) {
		return Config{}, ErrConfig
	}
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return Config{}, ErrConfig
	}
	c := Config{}
	seen := make(map[string]bool, 2)
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return Config{}, ErrConfig
		}
		seen[key] = true
		var raw json.RawMessage
		if err := d.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Config{}, ErrConfig
		}
		switch key {
		case "version":
			err = json.Unmarshal(raw, &c.Version)
		case "tab":
			err = json.Unmarshal(raw, &c.Tab)
			if c.Tab == "" {
				return Config{}, ErrConfig
			}
		default:
			return Config{}, ErrConfig
		}
		if err != nil {
			return Config{}, ErrConfig
		}
	}
	if token, err = d.Token(); err != nil || token != json.Delim('}') {
		return Config{}, ErrConfig
	}
	if _, err = d.Token(); err != io.EOF || c.Validate() != nil {
		return Config{}, ErrConfig
	}
	return c, nil
}

// LoadConfig reads only path, without environment expansion, home-directory
// expansion, or discovery. Paths and operating-system errors are not returned.
// The caller must control the file and its ancestors during the read.
func LoadConfig(path string) (Config, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return Config{}, ErrConfigRead
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxConfigBytes {
		return Config{}, ErrConfigRead
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, ErrConfigRead
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxConfigBytes {
		return Config{}, ErrConfigRead
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxConfigBytes+1))
	if err != nil {
		return Config{}, ErrConfigRead
	}
	return ParseConfig(data)
}

func configTab(name string) (tui.Tab, bool) {
	for _, tab := range tui.Tabs() {
		if strings.ToLower(tab.String()) == name {
			return tab, true
		}
	}
	return 0, false
}
