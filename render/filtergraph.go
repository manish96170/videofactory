package render

import (
	"fmt"
	"strconv"

	"github.com/manish96170/videofactory/domain"
)

// extraInput is one additional ffmpeg input beyond the implicit
// "color" filter source used for the base layer (e.g. an Image
// primitive's still image, looped for the shot's duration).
type extraInput struct {
	args []string // e.g. ["-loop", "1", "-t", "2.000000", "-i", "/path/to/image.png"]
}

// filterPlan is everything buildFilterGraph needs the caller to turn
// into a full ffmpeg command line.
type filterPlan struct {
	inputs        []extraInput
	filterComplex string
	outputLabel   string // without brackets, e.g. "v3"
	// cleanup removes any temporary files buildFilterGraph created
	// (e.g. a text content file for "textfile=", a sanitized copy of
	// FontPath — see the Text case below and DECISIONS.md ADR-016's
	// amendment). The caller must call every entry after the ffmpeg
	// command finishes, success or failure.
	cleanup []func()
}

func (p *filterPlan) runCleanup() {
	for _, fn := range p.cleanup {
		fn()
	}
}

// buildFilterGraph builds the ffmpeg filter graph for shot's Primitives
// (PHASES.md Phase 03: basic background/image/text/transition
// primitives, composed in Primitives order; a Background, if present,
// is always the base layer regardless of its position in the list).
//
// Supported today: at most one Background primitive, any number of
// Image/Text primitives (composited bottom-to-top in list order), and
// at most one Transition primitive (a whole-shot fade, applied last).
// Any other PrimitiveType returns an explicit error rather than being
// silently skipped — PHASES.md's later-phase primitives (CodeEditor,
// Terminal, DeveloperAvatar, ...) aren't renderable deterministically
// yet, and silently dropping them would produce a shot missing content
// without any indication why.
func buildFilterGraph(shot domain.Shot, settings Settings) (plan filterPlan, err error) {
	if err := validateSettings(settings); err != nil {
		return filterPlan{}, err
	}
	if err := validateShotPrimitiveIDs(shot); err != nil {
		return filterPlan{}, err
	}

	var cleanups []func()
	defer func() {
		if err != nil {
			for _, fn := range cleanups {
				fn()
			}
		}
	}()

	durSeconds := formatSeconds(shot.Duration.Seconds())

	var backgroundColor = "0x000000"
	var transitionPrim *domain.Primitive
	backgroundSeen := false

	for i := range shot.Primitives {
		p := &shot.Primitives[i]
		switch p.Type {
		case domain.PrimitiveTypeBackground:
			if backgroundSeen {
				return filterPlan{}, fmt.Errorf("render: shot %q has more than one Background primitive", shot.ID)
			}
			backgroundSeen = true
			colorStr, err := stringPropOr(p.Properties, "color", "#000000")
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q background primitive %q: %w", shot.ID, p.ID, err)
			}
			hex, err := parseHexColor(colorStr)
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q background primitive %q: %w", shot.ID, p.ID, err)
			}
			backgroundColor = hex
		case domain.PrimitiveTypeTransition:
			if transitionPrim != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q has more than one Transition primitive", shot.ID)
			}
			transitionPrim = p
		case domain.PrimitiveTypeImage, domain.PrimitiveTypeText:
			// handled in the composition pass below
		default:
			return filterPlan{}, fmt.Errorf("render: shot %q primitive %q: PrimitiveType %q is not supported by the deterministic renderer", shot.ID, p.ID, p.Type)
		}
	}

	plan = filterPlan{}
	var fc []string

	cur := "base"
	fc = append(fc, fmt.Sprintf("color=c=%s:size=%dx%d:rate=%d:duration=%s[base]",
		backgroundColor, settings.Width, settings.Height, settings.FPS, durSeconds))

	labelN := 0
	nextLabel := func() string {
		labelN++
		return fmt.Sprintf("v%d", labelN)
	}

	for i := range shot.Primitives {
		p := &shot.Primitives[i]
		switch p.Type {
		case domain.PrimitiveTypeImage:
			path, err := stringProp(p.Properties, "assetPath")
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q image primitive %q: %w", shot.ID, p.ID, err)
			}
			if err := checkAssetFile(path); err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q image primitive %q: %w", shot.ID, p.ID, err)
			}
			bounds, err := boundsProp(p.Properties, "bounds")
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q image primitive %q: %w", shot.ID, p.ID, err)
			}
			if bounds != nil {
				if err := validateBounds(*bounds); err != nil {
					return filterPlan{}, fmt.Errorf("render: shot %q image primitive %q: %w", shot.ID, p.ID, err)
				}
			}
			x, y, w, h := boundsToPixelBox(bounds, settings)

			inputIdx := len(plan.inputs)
			plan.inputs = append(plan.inputs, extraInput{args: []string{"-loop", "1", "-t", durSeconds, "-i", path}})

			scaled := nextLabel()
			fc = append(fc, fmt.Sprintf("[%d:v]scale=%d:%d[%s]", inputIdx, w, h, scaled))
			out := nextLabel()
			fc = append(fc, fmt.Sprintf("[%s][%s]overlay=%d:%d[%s]", cur, scaled, x, y, out))
			cur = out

		case domain.PrimitiveTypeText:
			text, err := stringProp(p.Properties, "text")
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: %w", shot.ID, p.ID, err)
			}
			colorStr, err := stringPropOr(p.Properties, "color", "#FFFFFF")
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: %w", shot.ID, p.ID, err)
			}
			hex, err := parseHexColor(colorStr)
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: %w", shot.ID, p.ID, err)
			}
			fontSize, err := floatPropOr(p.Properties, "fontSize", 48)
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: %w", shot.ID, p.ID, err)
			}
			if err := requireFinite("fontSize", fontSize); err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: %w", shot.ID, p.ID, err)
			}
			if fontSize <= 0 {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: fontSize must be positive, got %v", shot.ID, p.ID, fontSize)
			}
			if settings.FontPath == "" {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: no FontPath configured (Settings.FontPath is empty and no default font was found)", shot.ID, p.ID)
			}
			if err := checkAssetFile(settings.FontPath); err != nil {
				return filterPlan{}, fmt.Errorf("render: configured FontPath: %w", err)
			}

			bounds, err := boundsProp(p.Properties, "bounds")
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: %w", shot.ID, p.ID, err)
			}
			var xExpr, yExpr string
			if bounds != nil {
				if err := validateBounds(*bounds); err != nil {
					return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: %w", shot.ID, p.ID, err)
				}
				x, y, _, _ := boundsToPixelBox(bounds, settings)
				xExpr, yExpr = strconv.Itoa(x), strconv.Itoa(y)
			} else {
				// Centered by default, using drawtext's own w/h
				// (frame) and text_w/text_h (rendered text) expression
				// variables.
				xExpr, yExpr = "(w-text_w)/2", "(h-text_h)/2"
			}

			// Both the text content and the font path are written
			// through controlled, Go-generated temp files/paths rather
			// than escaped and embedded directly into the
			// filter_complex string. See DECISIONS.md ADR-016's
			// amendment: a close-quote/escape/reopen-quote sequence for
			// an embedded apostrophe parses without error but silently
			// produces *empty* rendered text — a correctness bug, not
			// just a security one — so arbitrary caller-controlled
			// strings are kept out of the filtergraph text entirely.
			textFilePath, textCleanup, err := writeTempTextFile(text)
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: shot %q text primitive %q: %w", shot.ID, p.ID, err)
			}
			cleanups = append(cleanups, textCleanup)

			safeFontPath, fontCleanup, err := prepareSafeFontFile(settings.FontPath)
			if err != nil {
				return filterPlan{}, fmt.Errorf("render: configured FontPath: %w", err)
			}
			cleanups = append(cleanups, fontCleanup)

			out := nextLabel()
			fc = append(fc, fmt.Sprintf(
				"[%s]drawtext=fontfile=%s:textfile=%s:fontcolor=%s:fontsize=%s:x=%s:y=%s:expansion=none[%s]",
				cur, safeFontPath, textFilePath, hex, formatFloat(fontSize), xExpr, yExpr, out,
			))
			cur = out
		}
	}

	if transitionPrim != nil {
		style, err := stringPropOr(transitionPrim.Properties, "style", "fade-in")
		if err != nil {
			return filterPlan{}, fmt.Errorf("render: shot %q transition primitive %q: %w", shot.ID, transitionPrim.ID, err)
		}
		var dir string
		switch style {
		case "fade-in":
			dir = "in"
		case "fade-out":
			dir = "out"
		default:
			return filterPlan{}, fmt.Errorf("render: shot %q transition primitive %q: unsupported style %q (want \"fade-in\" or \"fade-out\")", shot.ID, transitionPrim.ID, style)
		}
		out := nextLabel()
		fc = append(fc, fmt.Sprintf("[%s]fade=t=%s:st=0:d=%s[%s]", cur, dir, durSeconds, out))
		cur = out
	}

	plan.filterComplex = joinFilters(fc)
	plan.outputLabel = cur
	plan.cleanup = cleanups
	return plan, nil
}

func joinFilters(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ";"
		}
		out += p
	}
	return out
}

// validateShotPrimitiveIDs rejects a shot whose Primitives have an
// empty or duplicate ID, since manifest construction (manifest.go)
// reuses each Primitive's own ID directly as its VisualElement's ID:
// an empty or duplicate Primitive ID would silently produce an invalid
// or non-unique "stable rendered element identity" (PHASES.md section
// 4.6.1).
func validateShotPrimitiveIDs(shot domain.Shot) error {
	seen := make(map[domain.ID]struct{}, len(shot.Primitives))
	for _, p := range shot.Primitives {
		if p.ID.IsZero() {
			return fmt.Errorf("render: shot %q has a primitive with an empty ID", shot.ID)
		}
		if _, dup := seen[p.ID]; dup {
			return fmt.Errorf("render: shot %q has more than one primitive with ID %q", shot.ID, p.ID)
		}
		seen[p.ID] = struct{}{}
	}
	return nil
}

func formatSeconds(seconds float64) string {
	return strconv.FormatFloat(seconds, 'f', 6, 64)
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
