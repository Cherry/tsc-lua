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

// Substring arguments: these parameters are only measured, indexed, sliced or passed on, so a
// caller's s[i:j] reaches them without being copied.
func startsWithDigits(text string, n int) bool {
	if len(text) < n {
		return false
	}
	for i := 0; i < n; i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

func countByte(text string, b byte) int {
	n := 0
	for i := 0; i < len(text); i++ {
		if text[i] == b {
			n++
		}
	}
	return n
}

func describe(label string, text string, from int) string {
	rest := text[from:]
	return fmt.Sprint(label, ":", len(text), countByte(text, 'l'), countByte(text[from:], 'l'), startsWithDigits(text, 2), strings.Index(text[from:], "l"), rest)
}

type reader struct{ calls int }

func (r *reader) first(text string) byte {
	r.calls++
	if len(text) == 0 {
		return 0
	}
	return text[0]
}

// source.text is a capped field: cutting it to a prefix and restoring it copies nothing.
type source struct {
	name string
	text string
	end  int
}

func (s *source) setText(text string) {
	s.text = text
	s.end = len(text)
}

func (s *source) whole() string { return s.text }

func (s *source) at(i int) byte {
	if i < len(s.text) {
		return s.text[i]
	}
	return 0
}

func (s *source) rest(from int) string { return s.text[from:] }

func (s *source) window(from int) (rune, int, bool, int, int) {
	r, size := utf8.DecodeRuneInString(s.text[from:])
	return r, size, strings.HasPrefix(s.text[from:], "llo"), strings.IndexByte(s.text[from:], 'o'), strings.LastIndex(s.text[:from], "l")
}

func capped() {
	src := &source{name: "src"}
	src.setText("hello, wörld: all of it")
	fmt.Println(src.whole(), src.end, len(src.text), src.at(4), src.at(40))
	saved := src.text
	src.text = src.text[:9]
	fmt.Println(len(src.text), src.at(8), src.at(9), src.rest(7), countByte(src.text, 'l'), countByte(src.text[3:], 'l'))
	fmt.Println(src.window(2))
	fmt.Println(src.window(8))
	other := &source{name: "other"}
	other.setText(src.text)
	fmt.Println(other.end, other.whole(), other.text == src.text)
	clone := *src
	src.text = src.text[:5]
	fmt.Println(len(clone.text), clone.whole(), len(src.text), src.whole(), src.text[1:3])
	src.text += "!"
	fmt.Println(src.text, len(src.text))
	src.text = saved
	other.setText(src.text[7:])
	fmt.Println(src.whole(), other.whole(), other.end)
	var empty source
	fmt.Println(len(empty.text), empty.whole() == "", empty.at(0))
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

	fmt.Println(strings.LastIndex(s[:20], "l"), strings.LastIndex(s[3:9], "h"), strings.LastIndex(s[5:], "zz"), strings.LastIndex(s[2:6], ""), strings.LastIndex(s[:3], "abc"))
	fmt.Println(startsWithDigits(s[3:], 3), startsWithDigits(s[3:5], 3), startsWithDigits(s, 1), startsWithDigits(view[1:], 1))
	fmt.Println(countByte(s[7:], 'l'), countByte(s[:8], 'l'), countByte(s, 'l'), countByte(view, 'l'))
	fmt.Println(describe("mid", s[4:14], 4), describe("all", s, 20))
	r1 := &reader{}
	fmt.Println(r1.first(s[2:]), r1.first(s[len(s):]), r1.first(s), r1.calls)
	check := func(f func(string, byte) int) int { return f(s[7:10], 'l') }
	fmt.Println(check(countByte))
	capped()
}
