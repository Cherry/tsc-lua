package main

import (
	"fmt"
	"slices"
)

type Info struct {
	Flags int
	Name  string
}

func appendAll[T any](dst []T, src ...T) []T { return append(dst, src...) }

func main() {
	orig := []Info{{1, "a"}, {2, "b"}, {3, "c"}}
	cloned := slices.Clone(orig)
	cloned[0].Flags = 100
	fmt.Println(orig[0].Flags, cloned[0].Flags)

	small := make([]Info, 2, 2)
	small[0].Name = "x"
	grown := append(small, Info{9, "z"})
	grown[0].Name = "changed"
	fmt.Println(small[0].Name, grown[0].Name)

	dst := make([]Info, 3)
	copy(dst, orig)
	dst[1].Flags = 77
	fmt.Println(orig[1].Flags, dst[1].Flags)

	shift := []Info{{1, "p"}, {2, "q"}, {3, "r"}, {4, "s"}}
	copy(shift[1:], shift[:3])
	fmt.Println(shift)

	generic := appendAll([]Info{}, orig...)
	generic[2].Name = "gen"
	fmt.Println(orig[2].Name, generic[2].Name)

	arrs := [][2]int{{1, 2}, {3, 4}}
	arrClone := slices.Clone(arrs)
	arrClone[0][0] = 99
	fmt.Println(arrs[0][0], arrClone[0][0])
}
