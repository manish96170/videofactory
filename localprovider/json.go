package localprovider

import (
	"encoding/json"
	"math"
	"unicode/utf8"

	"github.com/manish96170/videofactory/worker"
)

// Preflight plain JSON trees before Marshal: depth, total nodes and worst-case
// escaped bytes are bounded. Custom marshalers and cyclic containers are not
// accepted. This is deliberately conservative near the 1 MiB wire limit.
func jsonTree(v any, budget *int, depth int) bool {
	*budget -= 32
	if *budget < 0 || depth > 32 {
		return false
	}
	switch v := v.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return !math.IsNaN(v) && !math.IsInf(v, 0)
	case float32:
		return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0)
	case string:
		if !utf8.ValidString(v) || len(v) > *budget/6 {
			return false
		}
		*budget -= len(v) * 6
		return true
	case map[string]any:
		for k, value := range v {
			if !jsonTree(k, budget, depth+1) || !jsonTree(value, budget, depth+1) {
				return false
			}
		}
		return true
	case []any:
		for _, value := range v {
			if !jsonTree(value, budget, depth+1) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func copyInput(in map[string]any) (map[string]any, error) {
	budget := worker.MaxStdinBytes - 4096
	if !jsonTree(in, &budget, 0) {
		return nil, ErrRequest
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, ErrRequest
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, ErrRequest
	}
	return out, nil
}
