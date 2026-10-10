package resource

import (
	"math"
	"math/big"
	"strconv"
)

func rationalUnits(value float64, scale int64, ceil bool) (int64, error) {
	if !finiteNonnegative(value) {
		return 0, ErrInvalidRequest
	}
	// Use the shortest round-trippable decimal declaration. E.g. 0.1 is
	// 100000 micro-units, while Nextafter(0.1,+Inf) still rounds upward.
	r, ok := new(big.Rat).SetString(strconv.FormatFloat(value, 'g', -1, 64))
	if !ok {
		return 0, ErrInvalidRequest
	}
	r.Mul(r, new(big.Rat).SetInt64(scale))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	if ceil && rem.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, ErrOverflow
	}
	return q.Int64(), nil
}

func checkedAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// ApplySafetyMargin computes ceil(bytes*(100+percent)/100) with integer
// arithmetic; intermediates may exceed int64 but the result may not.
func ApplySafetyMargin(bytes, percent int64) (int64, error) {
	if bytes < 0 || percent < 0 {
		return 0, ErrInvalidRequest
	}
	p := new(big.Int).Add(big.NewInt(percent), big.NewInt(100))
	p.Mul(p, big.NewInt(bytes))
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(p, big.NewInt(100), rem)
	if rem.Sign() != 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() {
		return 0, ErrOverflow
	}
	return q.Int64(), nil
}
