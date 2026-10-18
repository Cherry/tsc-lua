// Mirrors the test runner's option variant expansion: maps with interface keys holding enum
// values, maps.Clone snapshots of a mutated map, and slices.Collect over maps.Values.
package main

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

type scriptTarget int32

type optionValue any

type configuration = map[string]string

func valueOf(option, value string) optionValue {
	switch option {
	case "target":
		targets := map[string]scriptTarget{"es5": 1, "es2015": 2, "es6": 2, "esnext": 99}
		return targets[strings.ToLower(value)]
	case "strict":
		return value == "true"
	}
	return value
}

func splitOptionValues(value, option string) []string {
	variations := make(map[optionValue]string)
	for s := range strings.SplitSeq(value, ",") {
		s = strings.TrimSpace(s)
		v := valueOf(option, s)
		if _, ok := variations[v]; !ok {
			variations[v] = s
		}
	}
	result := slices.Collect(maps.Values(variations))
	sort.Strings(result)
	return result
}

func expand(configurations *[]configuration, entries [][]string, index int, state configuration) {
	if index >= len(entries) {
		*configurations = append(*configurations, maps.Clone(state))
		return
	}
	key := entries[index][0]
	for _, entry := range entries[index][1:] {
		state[key] = entry
		expand(configurations, entries, index+1, state)
	}
}

func main() {
	fmt.Println(splitOptionValues("esnext, es2015, es6", "target"))
	fmt.Println(splitOptionValues("true, false", "strict"))
	entries := [][]string{
		append([]string{"target"}, splitOptionValues("esnext, es2015", "target")...),
		append([]string{"strict"}, splitOptionValues("true, false", "strict")...),
	}
	var configurations []configuration
	expand(&configurations, entries, 0, make(configuration))
	for _, c := range configurations {
		fmt.Println(c["target"], c["strict"])
	}
}
