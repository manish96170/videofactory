package resource

import (
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
)

const (
	GB         int64 = 1_000_000_000
	MicroUnits int64 = 1_000_000
)

var (
	ErrInvalidConfig      = errors.New("resource: invalid_config")
	ErrInvalidRequest     = errors.New("resource: invalid_request")
	ErrOverBudget         = errors.New("resource: over_budget")
	ErrIncompatible       = errors.New("resource: incompatible_backend")
	ErrQueueFull          = errors.New("resource: queue_full")
	ErrHistoryFull        = errors.New("resource: history_full")
	ErrInvalidProfile     = errors.New("resource: invalid_profile")
	ErrLocalIneligible    = errors.New("resource: local_ineligible")
	ErrInvalidMeasurement = errors.New("resource: invalid_measurement")
	ErrOverflow           = errors.New("resource: overflow")
)

// AcceleratorFacts are supplied hardware facts. Unknown facts require zero
// worker GPU budget. Capacity is logical units, NOT bytes/kernel VRAM limits.
type AcceleratorFacts struct {
	Known         bool                      `json:"known"`
	Backend       domain.AcceleratorBackend `json:"backend"`
	UnifiedMemory bool                      `json:"unifiedMemory"`
	Capacity      float64                   `json:"capacity"`
}

// Config contains only copied values; no detection, runtime.NumCPU assumptions,
// probes or callbacks. CPUCapacity is explicit physical CPU allocation units;
// WorkerCPU/WorkerGPU are logical quotas no greater than supplied capacities.
type Config struct {
	TotalRAMBytes        int64            `json:"totalRamBytes"`
	SystemReserveBytes   int64            `json:"systemReserveBytes"`
	UserReserveBytes     int64            `json:"userReserveBytes"`
	DirectorReserveBytes int64            `json:"directorReserveBytes"`
	WorkerRAMBytes       int64            `json:"workerRamBytes"`
	CPUCapacity          float64          `json:"cpuCapacity"`
	WorkerCPU            float64          `json:"workerCpu"`
	WorkerGPU            float64          `json:"workerGpu"`
	Accelerator          AcceleratorFacts `json:"accelerator"`
	HardwareClass        string           `json:"hardwareClass"` // caller-supplied nonsecret stable class, not host ID/path
	MaxParallel          int              `json:"maxParallel"`
	MaxHeavy             int              `json:"maxHeavy"`
	QueueLimit           int              `json:"queueLimit"`
	HeavyRAMThreshold    int64            `json:"heavyRamThreshold"`
}

// ReferenceConfig is a data profile, not runtime auto-configuration. The caller
// MUST supply CPU facts/limits and accelerator facts/limit for the target host.
func ReferenceConfig(cpuCapacity, workerCPU float64, accelerator AcceleratorFacts, workerGPU float64, hardwareClass string) Config {
	return Config{TotalRAMBytes: 16 * GB, SystemReserveBytes: 3 * GB, UserReserveBytes: 4 * GB,
		DirectorReserveBytes: 3 * GB / 2, WorkerRAMBytes: 15 * GB / 2, CPUCapacity: cpuCapacity,
		WorkerCPU: workerCPU, Accelerator: accelerator, WorkerGPU: workerGPU,
		HardwareClass: hardwareClass, MaxParallel: 2, MaxHeavy: 1, QueueLimit: 64, HeavyRAMThreshold: 2 * GB}
}

func finiteNonnegative(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 }

// Invalid UTF-8 cannot round-trip exact string keys through encoding/json.
func blank(s string) bool { return !utf8.ValidString(s) || strings.TrimSpace(s) == "" }
func knownBackend(b domain.AcceleratorBackend) bool {
	switch b {
	case domain.BackendNone, domain.BackendMetal, domain.BackendCUDA, domain.BackendROCm, domain.BackendVulkan:
		return true
	}
	return false
}

// quantize uses exact rational arithmetic on the float's round-trippable decimal
// declaration so multiplication cannot erase fractions or overflow conversion.
func quantize(n float64, up bool) (int64, error) {
	if !finiteNonnegative(n) {
		return 0, ErrInvalidRequest
	}
	return rationalUnits(n, MicroUnits, up)
}

func (c Config) Validate() error {
	if c.TotalRAMBytes <= 0 || c.WorkerRAMBytes <= 0 || c.SystemReserveBytes < 0 || c.UserReserveBytes < 0 || c.DirectorReserveBytes < 0 ||
		c.MaxParallel <= 0 || c.MaxHeavy <= 0 || c.MaxHeavy > c.MaxParallel || c.QueueLimit < 0 || c.HeavyRAMThreshold <= 0 || blank(c.HardwareClass) {
		return ErrInvalidConfig
	}
	remaining := c.TotalRAMBytes
	for _, reserve := range []int64{c.SystemReserveBytes, c.UserReserveBytes, c.DirectorReserveBytes} {
		if reserve > remaining {
			return ErrInvalidConfig
		}
		remaining -= reserve
	}
	if c.WorkerRAMBytes > remaining {
		return ErrInvalidConfig
	}
	physicalCPU, e1 := quantize(c.CPUCapacity, false)
	cpu, e2 := quantize(c.WorkerCPU, false)
	physicalGPU, e3 := quantize(c.Accelerator.Capacity, false)
	gpu, e4 := quantize(c.WorkerGPU, false)
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil || physicalCPU <= 0 || cpu <= 0 || c.WorkerCPU > c.CPUCapacity || cpu > physicalCPU || c.WorkerGPU > c.Accelerator.Capacity || gpu > physicalGPU {
		return ErrInvalidConfig
	}
	if !c.Accelerator.Known {
		if c.WorkerGPU != 0 || c.Accelerator.Capacity != 0 || c.Accelerator.UnifiedMemory || (c.Accelerator.Backend != "" && c.Accelerator.Backend != domain.BackendUnknown) {
			return ErrInvalidConfig
		}
	} else if !knownBackend(c.Accelerator.Backend) || (c.Accelerator.Backend == domain.BackendNone && (c.WorkerGPU != 0 || c.Accelerator.Capacity != 0 || c.Accelerator.UnifiedMemory)) {
		return ErrInvalidConfig
	}
	return nil
}

// Counters use integer bytes/micro-units internally and on snapshots, avoiding
// fractional accumulation/release drift. Slots/Heavy count full allocations.
type Counters struct {
	RAMBytes int64 `json:"ramBytes"`
	CPUUnits int64 `json:"cpuUnits"`
	GPUUnits int64 `json:"gpuUnits"`
	Slots    int   `json:"slots"`
	Heavy    int   `json:"heavy"`
}

func totals(c Config) Counters {
	cpu, _ := quantize(c.WorkerCPU, false)
	gpu, _ := quantize(c.WorkerGPU, false)
	return Counters{c.WorkerRAMBytes, cpu, gpu, c.MaxParallel, c.MaxHeavy}
}

func normalized(c Config, r Request) (Request, Counters, error) {
	if r.RAMBytes < 0 {
		return Request{}, Counters{}, ErrInvalidRequest
	}
	cpu, e1 := quantize(r.CPU, true)
	gpu, e2 := quantize(r.GPU, true)
	if e1 != nil || e2 != nil {
		return Request{}, Counters{}, errors.Join(ErrInvalidRequest, e1, e2)
	}
	if r.GPU > 0 {
		if !c.Accelerator.Known || r.Backend == domain.BackendNone || !knownBackend(r.Backend) || r.Backend != c.Accelerator.Backend {
			return Request{}, Counters{}, ErrIncompatible
		}
	} else if r.Backend != "" && r.Backend != domain.BackendNone {
		return Request{}, Counters{}, ErrIncompatible
	}
	r.Heavy = r.Heavy || r.GPU > 0 || r.RAMBytes >= c.HeavyRAMThreshold
	q := Counters{RAMBytes: r.RAMBytes, CPUUnits: cpu, GPUUnits: gpu, Slots: 1}
	if r.Heavy {
		q.Heavy = 1
	}
	t := totals(c)
	if !fits(q, t) {
		return Request{}, Counters{}, ErrOverBudget
	}
	return r, q, nil
}

// ValidateRequest rejects permanent failures without reserving or queueing.
// The returned request has non-bypassable Heavy classification applied.
func ValidateRequest(c Config, r Request) (Request, error) {
	if err := c.Validate(); err != nil {
		return Request{}, err
	}
	n, _, err := normalized(c, r)
	return n, err
}

func fits(q, a Counters) bool {
	return q.RAMBytes <= a.RAMBytes && q.CPUUnits <= a.CPUUnits && q.GPUUnits <= a.GPUUnits && q.Slots <= a.Slots && q.Heavy <= a.Heavy
}
func subtract(a, b Counters) Counters {
	return Counters{a.RAMBytes - b.RAMBytes, a.CPUUnits - b.CPUUnits, a.GPUUnits - b.GPUUnits, a.Slots - b.Slots, a.Heavy - b.Heavy}
}
func add(a, b Counters) Counters {
	return Counters{a.RAMBytes + b.RAMBytes, a.CPUUnits + b.CPUUnits, a.GPUUnits + b.GPUUnits, a.Slots + b.Slots, a.Heavy + b.Heavy}
}
