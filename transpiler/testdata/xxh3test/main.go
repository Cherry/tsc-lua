package main

import (
	"fmt"
	"strings"

	"github.com/zeebo/xxh3"
)

func main() {
	var sb strings.Builder
	for i := 0; i < 1600; i++ {
		sb.WriteByte(byte(i*7 + i/13))
	}
	all := sb.String()
	for _, n := range []int{0, 1, 2, 3, 4, 5, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 96, 97, 127, 128, 129, 160, 200, 239, 240, 241, 255, 256, 500, 1024, 1025, 1088, 1100, 1500, 1600} {
		s := all[:n]
		h := xxh3.HashString128(s)
		fmt.Printf("%d %016x %016x %016x %x\n", n, h.Hi, h.Lo, xxh3.HashString(s), xxh3.Hash128([]byte(s)).Bytes())
	}
}
