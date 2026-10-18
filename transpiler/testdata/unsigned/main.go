// 64-bit unsigned comparisons against constants, including values at or above 2^63 (negative
// as Lua integers).
package main

import (
	"fmt"
	"math"
)

func main() {
	values := []uint64{0, 1, 1 << 48, 1<<48 + 1, math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64}
	for _, v := range values {
		fmt.Println(v,
			v < 1<<48, v <= 1<<48, v > 1<<48, v >= 1<<48,
			1<<48 < v, 1<<48 <= v, 1<<48 > v, 1<<48 >= v,
			v < 0x7fffffffffffffff, v > 5,
			v >= 1<<48 && v < 1<<48+(1<<24))
	}
}
