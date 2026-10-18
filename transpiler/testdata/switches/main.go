// Large integer switches, which the transpiler emits as binary decision trees.
package main

import "fmt"

type kind int16

const (
	kA kind = iota - 3
	kB
	kC
	kD
	kE
	kF
	kG
	kH
	kI
	kJ
	kK
	kL
)

func classify(k kind) string {
	switch k {
	case kA, kL:
		return "edge"
	case kB:
		return "b"
	case kC, kE, kG:
		return "odd"
	case kD:
		// Empty body: falls out of the switch.
	case kF:
		if k > 0 {
			break
		}
		return "unreachable"
	case kH:
		return "h"
	case kI, kJ:
		return "ij"
	default:
		return "other"
	}
	return "fell out"
}

func noDefault(b uint8) int {
	n := 0
	switch b {
	case 0:
		n = 1
	case 1, 2, 3:
		n = 2
	case 10:
		n = 3
	case 200:
		n = 4
	case 255:
		n = 5
	case 100, 101:
		n = 6
	}
	return n
}

// The scanner's shape: fallthrough chains, a default between cases, and fallthrough into it.
func scan(c rune) string {
	out := ""
	switch c {
	case '\r':
		out += "cr+"
		fallthrough
	case '\n':
		out += "nl"
	case '!', '=':
		out += "op"
		if c == '=' {
			break
		}
		out += "!"
	default:
		out += "other"
	case 'a', 'b', 'c':
		out += "letter"
		fallthrough
	case 'x':
		out += "+x"
		fallthrough
	case 'y':
		out += "+y"
	case '0':
		x := 1
		out += fmt.Sprint(x)
		fallthrough
	case '1':
		y := 2
		out += fmt.Sprint(y)
	case '9':
		out += "nine"
		fallthrough
	case '8':
		out += "+eight"
		fallthrough
	case '7':
		out += "+seven"
	}
	return out
}

func main() {
	for _, c := range "\r\n!=abcxyz01789?" {
		fmt.Printf("%q %s\n", c, scan(c))
	}
	for k := kind(-6); k <= 12; k++ {
		fmt.Println(k, classify(k))
	}
	for _, b := range []uint8{0, 1, 2, 3, 4, 9, 10, 11, 99, 100, 101, 102, 199, 200, 201, 254, 255} {
		fmt.Println(b, noDefault(b))
	}
}
