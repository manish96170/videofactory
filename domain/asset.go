package domain

// AssetKind classifies an Asset's media type.
type AssetKind string

const (
	AssetKindImage AssetKind = "image"
	AssetKindVideo AssetKind = "video"
	AssetKindAudio AssetKind = "audio"
	AssetKindFont  AssetKind = "font"
	AssetKindOther AssetKind = "other"
)

// Asset is a reusable piece of generated or sourced media.
//
// URI is a storage reference (relative path, content hash, or remote
// locator). It must never be a machine-specific absolute path committed to
// the repository (PHASES.md section 0/17).
type Asset struct {
	ID       ID             `json:"id"`
	Kind     AssetKind      `json:"kind"`
	URI      string         `json:"uri"`
	Metadata map[string]any `json:"metadata,omitempty"`
}
