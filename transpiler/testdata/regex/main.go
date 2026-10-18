package main

import (
	"fmt"
	"regexp"
	"strings"
)

func main() {
	re := regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)
	fmt.Println(re.FindStringSubmatch("v1.22.333-beta.1"))
	fmt.Println(re.MatchString("1.2"), re.MatchString("10.20.30"))
	re2 := regexp.MustCompile(`(?i)hello\s+(world|there)`)
	fmt.Println(re2.ReplaceAllString("Hello   World and hello there", "<$1>"))
	fmt.Println(strings.Join(regexp.MustCompile(`\s*,\s*`).Split("a , b,c ,d", -1), "|"))
	fmt.Printf("%q\n", regexp.MustCompile(`[^a-z]+`).FindAllString("abc123def45g", -1))
}
