package domain

// AcceleratorBackend identifies an execution API, not a vendor/model or VRAM
// limit. Metal and CUDA are not interchangeable. Empty/unknown is not CPU-only.
type AcceleratorBackend string

const (
	BackendUnknown AcceleratorBackend = "unknown"
	BackendNone    AcceleratorBackend = "none"
	BackendMetal   AcceleratorBackend = "metal"
	BackendCUDA    AcceleratorBackend = "cuda"
	BackendROCm    AcceleratorBackend = "rocm"
	BackendVulkan  AcceleratorBackend = "vulkan"
)

// Workload is an exact comparable execution envelope/history key. Zero size or
// frames is permitted only by a profile explicitly supporting those zeros (e.g.
// nonvisual work). Offload/Precision/Quality are exact nonblank labels; use
// "none" for no offload. ChunkSize=0 means unchunked. No prompts/paths belong here.
type Workload struct {
	Precision string `json:"precision"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Frames    int    `json:"frames"`
	Quality   string `json:"quality"`
	Offload   string `json:"offload"`
	ChunkSize int    `json:"chunkSize"`
}

// IntRange is inclusive. A zero/zero range explicitly supports zero, not an
// unknown/unbounded range. Every dimension is checked independently.
type IntRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// WorkloadEnvelope declares combinations for which ONE conservative bound
// applies. All combinations in these ranges/lists must be supported; declare
// separate profiles if bounds or support differ by combination.
type WorkloadEnvelope struct {
	Precisions []string `json:"precisions"`
	Width      IntRange `json:"width"`
	Height     IntRange `json:"height"`
	Frames     IntRange `json:"frames"`
	Qualities  []string `json:"qualities"`
	Offloads   []string `json:"offloads"`
	ChunkSize  IntRange `json:"chunkSize"`
}

// ExecutionProfile is a trusted revisioned declaration, NOT a measured or
// model-generated guess. MemoryKnown=false/Trusted=false cannot admit local
// execution. Peak is max(LoadRAMBytes, ResidentRAMBytes+WorkspaceRAMBytes).
// Workspace bounds must cover run AND unload/cleanup temporary allocations.
// Bounds include unified GPU memory in RAM (no second subtraction). Discrete
// VRAM is NOT enforced: GPU is only a logical allocation quota.
type ExecutionProfile struct {
	ID                string             `json:"id"`
	Revision          string             `json:"revision"`
	Trusted           bool               `json:"trusted"`
	MemoryKnown       bool               `json:"memoryKnown"`
	LoadRAMBytes      int64              `json:"loadRamBytes"`
	ResidentRAMBytes  int64              `json:"residentRamBytes"`
	WorkspaceRAMBytes int64              `json:"workspaceRamBytes"`
	CPU               float64            `json:"cpu"`
	GPU               float64            `json:"gpu"`
	Backend           AcceleratorBackend `json:"backend"`
	Heavy             bool               `json:"heavy,omitempty"`
	Exclusive         bool               `json:"exclusive,omitempty"`
	Envelope          WorkloadEnvelope   `json:"envelope"`
}

// ModelRequirements version 1 binds execution profiles to a model revision.
// Changing weights/runtime/bounds requires a new revision, not history reuse.
// Optional legacy metadata remains compatible but cannot replace these bounds.
type ModelRequirements struct {
	Version           int                `json:"version"`
	Revision          string             `json:"revision"`
	ExecutionProfiles []ExecutionProfile `json:"executionProfiles"`
}

// EstimateProvenance is deterministic advisory evidence, never permission to
// load. The model manager recomputes from its frozen declarations/current budget.
type EstimateProvenance struct {
	ProviderID           string   `json:"providerId"`
	ModelID              string   `json:"modelId"`
	ModelRevision        string   `json:"modelRevision"`
	ProfileID            string   `json:"profileId"`
	ProfileRevision      string   `json:"profileRevision"`
	HardwareClass        string   `json:"hardwareClass"`
	Workload             Workload `json:"workload"`
	DeclaredPeakRAMBytes int64    `json:"declaredPeakRamBytes"`
	LegacyFloorRAMBytes  int64    `json:"legacyFloorRamBytes"`
	ObservedPeakRAMBytes *int64   `json:"observedPeakRamBytes,omitempty"`
	SafetyMarginPercent  int64    `json:"safetyMarginPercent"`
	HistoryVersion       uint64   `json:"historyVersion"`
	HistoryHash          string   `json:"historyHash,omitempty"`
}
