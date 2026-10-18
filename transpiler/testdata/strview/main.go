package main

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func scanWhile(s string, pos, end int, pred func(byte) bool) int {
	text := s[pos:end]
	i := 0
	for i < len(text) {
		if text[i] >= 128 || !pred(text[i]) {
			break
		}
		i++
	}
	return pos + i
}

func runesFrom(s string, pos int) []string {
	var out []string
	for pos < len(s) {
		r, size := utf8.DecodeRuneInString(s[pos:])
		out = append(out, fmt.Sprintf("%d:%c/%d", pos, r, size))
		pos += size
	}
	return out
}

func main() {
	s := "abc123 héllo 世界 \xff tail */ end"
	fmt.Println(scanWhile(s, 0, len(s), func(b byte) bool { return b >= 'a' && b <= 'z' }))
	fmt.Println(scanWhile(s, 3, 7, func(b byte) bool { return b >= '0' && b <= '9' }))
	fmt.Println(runesFrom(s, 7))
	fmt.Println(strings.HasPrefix(s[7:], "héllo"), strings.HasPrefix(s[7:9], "héllo"), strings.HasPrefix(s[len(s):], ""))
	fmt.Println(strings.IndexByte(s[3:], ' '), strings.IndexByte(s[3:5], ' '), strings.Index(s[10:], "*/"), strings.Index(s[10:20], "*/"))
	r, size := utf8.DecodeLastRuneInString(s[:16])
	fmt.Println(r, size)
	r, size = utf8.DecodeRuneInString(s[len(s):])
	fmt.Println(r, size)
	r, size = utf8.DecodeRuneInString(s[8:9])
	fmt.Println(r, size)

	view := s[4:20]
	sub := view[2:6]
	get := func(i int) byte { return view[i] }
	fmt.Println(len(view), view[0], sub, len(sub), get(3), view, strings.ToUpper(view[1:3]))
	var v2 = s[13:]
	fmt.Println(v2, len(v2), v2[len(v2)-1])
	nonView := s[1:4]
	nonView = nonView + "!"
	fmt.Println(nonView)
}
