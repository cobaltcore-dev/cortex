// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

// ANSI color codes - using background colors for differences
const (
	colorReset   = "\033[0m"
	colorGray    = "\033[90m"
	bgYellow     = "\033[43m\033[30m" // Yellow bg, black text
	bgCyan       = "\033[46m\033[30m" // Cyan bg, black text
	bgMagenta    = "\033[45m\033[30m" // Magenta bg, black text
	bgBlue       = "\033[44m\033[37m" // Blue bg, white text
	bgGreen      = "\033[42m\033[30m" // Green bg, black text
	bgRed        = "\033[41m\033[37m" // Red bg, white text
	bgRedMissing = "\033[41m\033[37m" // Red bg for "only in" indicators
)

var useColor bool

func main() {
	diffFlag := flag.String("diff", "", "Comma-separated list of resource names to compare (empty or omit = all)")
	noColorFlag := flag.Bool("no-color", false, "Disable colorized output")
	flag.Parse()
	useColor = !*noColorFlag && term.IsTerminal(int(os.Stdout.Fd())) //nolint:gosec // file descriptors are small numbers, uintptr->int safe in practice
	resources := readAndParseInput(os.Stdin)
	var names []string
	var selected []map[string]any
	if *diffFlag == "" {
		names = sortedMapKeys(resources)
		if len(names) < 2 {
			fatal("need at least 2 resources to compare")
		}
		for _, name := range names {
			selected = append(selected, resources[name])
		}
	} else {
		names = parseNames(*diffFlag)
		if len(names) < 2 {
			fatal("-diff requires at least 2 resource names")
		}
		selected = selectResourcesByName(resources, names)
	}
	var result map[string]any
	if len(names) == 2 {
		result = findDiff(names[0], selected[0], names[1], selected[1])
	} else {
		result = findMultiDiff(names, selected)
	}
	printColorized(result, names, 0)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func parseNames(commaSeparated string) []string {
	parts := strings.Split(commaSeparated, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if name := strings.TrimSpace(p); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func readAndParseInput(r io.Reader) map[string]map[string]any {
	data, err := io.ReadAll(r)
	if err != nil {
		fatal("error reading stdin: %v", err)
	}
	var list map[string]any
	if err := yaml.Unmarshal(data, &list); err != nil {
		fatal("error parsing yaml: %v", err)
	}
	items, ok := list["items"].([]any)
	if !ok {
		fatal("expected a Kubernetes List with 'items' field")
	}
	resources := make(map[string]map[string]any)
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		metadata, ok := itemMap["metadata"].(map[string]any)
		if !ok {
			continue
		}
		name, ok := metadata["name"].(string)
		if !ok {
			continue
		}
		resources[name] = itemMap
	}
	return resources
}

func selectResourcesByName(resources map[string]map[string]any, names []string) []map[string]any {
	selected := make([]map[string]any, 0, len(names))
	for _, name := range names {
		res, ok := resources[name]
		if !ok {
			fatal("resource %q not found", name)
		}
		selected = append(selected, res)
	}
	return selected
}

func col(c, s string) string {
	if !useColor {
		return s
	}
	return c + s + colorReset
}

func bgColorForName(name string, names []string) string {
	colors := []string{bgYellow, bgCyan, bgMagenta, bgBlue, bgGreen, bgRed}
	for i, n := range names {
		if n == name {
			return colors[i%len(colors)]
		}
	}
	return colorGray
}

func printColorized(result map[string]any, names []string, indent int) {
	keys := sortedKeys(result)
	prefix := strings.Repeat("    ", indent)
	for _, key := range keys {
		val := result[key]
		fmt.Printf("%s%s:", prefix, col(colorGray, key))
		switch v := val.(type) {
		case map[string]any:
			if eq, ok := v["_equal"]; ok {
				// Equal value - show in gray
				fmt.Printf(" %s\n", col(colorGray, fmt.Sprintf("%v", eq)))
			} else if isLeafDiff(v, names) {
				printLeafDiff(v, names)
			} else {
				fmt.Println()
				printColorized(v, names, indent+1)
			}
		case []string:
			fmt.Println()
			for _, s := range v {
				fmt.Printf("%s    - %s\n", prefix, s)
			}
		default:
			fmt.Printf(" %v\n", val)
		}
	}
}

func isLeafDiff(m map[string]any, names []string) bool {
	if _, ok := m["onlyIn"]; ok {
		return true
	}
	if _, ok := m["_equal"]; ok {
		return true
	}
	for k := range m {
		if strings.HasPrefix(k, "onlyIn_") {
			return true
		}
		isName := false
		for _, n := range names {
			if k == n {
				isName = true
				break
			}
		}
		if !isName {
			return false
		}
	}
	return len(m) > 0
}

func printLeafDiff(m map[string]any, names []string) {
	if eq, ok := m["_equal"]; ok {
		fmt.Printf(" %s\n", col(colorGray, fmt.Sprintf("%v", eq)))
		return
	}
	if onlyIn, ok := m["onlyIn"]; ok {
		val := m["value"]
		name := onlyIn.(string)
		fmt.Printf(" %s\n", col(bgRedMissing, fmt.Sprintf(" only in %s: %v ", name, val)))
		return
	}
	hasOnlyIn := false
	for k := range m {
		if strings.HasPrefix(k, "onlyIn_") {
			hasOnlyIn = true
			break
		}
	}
	if hasOnlyIn {
		fmt.Println()
		keys := sortedKeys(m)
		for _, k := range keys {
			if k == "_common" {
				// Common items in slice
				if items, ok := m[k].([]string); ok {
					for _, s := range items {
						fmt.Printf("        %s\n", col(colorGray, "- "+s))
					}
				}
			} else if strings.HasPrefix(k, "onlyIn_") {
				name := strings.TrimPrefix(k, "onlyIn_")
				vals := m[k]
				bg := bgColorForName(name, names)
				fmt.Printf("        %s\n", col(bg, fmt.Sprintf(" only in %s: ", name)))
				switch slice := vals.(type) {
				case []string:
					for _, s := range slice {
						fmt.Printf("            %s\n", col(bg, fmt.Sprintf(" - %s ", s)))
					}
				case []any:
					for _, s := range slice {
						fmt.Printf("            %s\n", col(bg, fmt.Sprintf(" - %v ", s)))
					}
				}
			}
		}
		return
	}
	fmt.Println()
	for _, name := range names {
		if val, ok := m[name]; ok {
			bg := bgColorForName(name, names)
			fmt.Printf("        %s\n", col(bg, fmt.Sprintf(" %s: %v ", name, val)))
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func findDiff(name1 string, res1 map[string]any, name2 string, res2 map[string]any) map[string]any {
	return diffMaps(name1, res1, name2, res2, "")
}

func diffMaps(name1 string, m1 map[string]any, name2 string, m2 map[string]any, path string) map[string]any {
	result := make(map[string]any)
	allKeys := make(map[string]bool)
	for k := range m1 {
		allKeys[k] = true
	}
	for k := range m2 {
		allKeys[k] = true
	}
	for key := range allKeys {
		if path == "" && key == "metadata" {
			metaDiff := diffMetadata(name1, m1, name2, m2)
			if len(metaDiff) > 0 {
				result[key] = metaDiff
			}
			continue
		}
		val1, has1 := m1[key]
		val2, has2 := m2[key]
		if !has1 {
			result[key] = map[string]any{"onlyIn": name2, "value": val2}
			continue
		}
		if !has2 {
			result[key] = map[string]any{"onlyIn": name1, "value": val1}
			continue
		}
		if reflect.DeepEqual(val1, val2) {
			result[key] = map[string]any{"_equal": val1}
			continue
		}
		m1Nested, ok1 := val1.(map[string]any)
		m2Nested, ok2 := val2.(map[string]any)
		if ok1 && ok2 {
			nested := diffMaps(name1, m1Nested, name2, m2Nested, path+"."+key)
			if len(nested) > 0 {
				result[key] = nested
			}
			continue
		}
		s1, ok1 := val1.([]any)
		s2, ok2 := val2.([]any)
		if ok1 && ok2 && isStringSlice(s1) && isStringSlice(s2) {
			sliceDiff := diffStringSlices(name1, s1, name2, s2)
			if len(sliceDiff) > 0 {
				result[key] = sliceDiff
			} else {
				result[key] = map[string]any{"_equal": val1}
			}
			continue
		}
		result[key] = map[string]any{name1: val1, name2: val2}
	}
	return result
}

func diffMetadata(name1 string, m1 map[string]any, name2 string, m2 map[string]any) map[string]any {
	result := make(map[string]any)
	meta1, _ := m1["metadata"].(map[string]any)
	meta2, _ := m2["metadata"].(map[string]any)
	if meta1 == nil && meta2 == nil {
		return nil
	}
	labels1, _ := meta1["labels"].(map[string]any)
	labels2, _ := meta2["labels"].(map[string]any)
	if labelDiff := diffMapFields(name1, labels1, name2, labels2); len(labelDiff) > 0 {
		result["labels"] = labelDiff
	}
	annot1, _ := meta1["annotations"].(map[string]any)
	annot2, _ := meta2["annotations"].(map[string]any)
	if annotDiff := diffMapFields(name1, annot1, name2, annot2); len(annotDiff) > 0 {
		result["annotations"] = annotDiff
	}
	return result
}

func diffMapFields(name1 string, m1 map[string]any, name2 string, m2 map[string]any) map[string]any {
	result := make(map[string]any)
	allKeys := make(map[string]bool)
	for k := range m1 {
		allKeys[k] = true
	}
	for k := range m2 {
		allKeys[k] = true
	}
	for key := range allKeys {
		val1, has1 := m1[key]
		val2, has2 := m2[key]
		if !has1 {
			result[key] = map[string]any{"onlyIn": name2, "value": val2}
			continue
		}
		if !has2 {
			result[key] = map[string]any{"onlyIn": name1, "value": val1}
			continue
		}
		if reflect.DeepEqual(val1, val2) {
			result[key] = map[string]any{"_equal": val1}
		} else {
			result[key] = map[string]any{name1: val1, name2: val2}
		}
	}
	return result
}

func diffStringSlices(name1 string, s1 []any, name2 string, s2 []any) map[string]any {
	set1 := make(map[string]bool)
	set2 := make(map[string]bool)
	for _, v := range s1 {
		set1[v.(string)] = true
	}
	for _, v := range s2 {
		set2[v.(string)] = true
	}
	var onlyIn1, onlyIn2, common []string
	for s := range set1 {
		if set2[s] {
			common = append(common, s)
		} else {
			onlyIn1 = append(onlyIn1, s)
		}
	}
	for s := range set2 {
		if !set1[s] {
			onlyIn2 = append(onlyIn2, s)
		}
	}
	sort.Strings(onlyIn1)
	sort.Strings(onlyIn2)
	sort.Strings(common)
	result := make(map[string]any)
	if len(common) > 0 {
		result["_common"] = common
	}
	if len(onlyIn1) > 0 {
		result["onlyIn_"+name1] = onlyIn1
	}
	if len(onlyIn2) > 0 {
		result["onlyIn_"+name2] = onlyIn2
	}
	return result
}

func findMultiDiff(names []string, resources []map[string]any) map[string]any {
	return multiDiffMaps(names, resources, "")
}

func multiDiffMaps(names []string, resources []map[string]any, path string) map[string]any {
	result := make(map[string]any)
	allKeys := make(map[string]bool)
	for _, res := range resources {
		for k := range res {
			allKeys[k] = true
		}
	}
	for key := range allKeys {
		if path == "" && key == "metadata" {
			metaDiff := multiDiffMetadata(names, resources)
			if len(metaDiff) > 0 {
				result[key] = metaDiff
			}
			continue
		}
		values := make([]any, len(resources))
		allPresent := true
		for i, res := range resources {
			val, ok := res[key]
			if !ok {
				allPresent = false
			}
			values[i] = val
		}
		if !allPresent {
			diff := make(map[string]any)
			for i, res := range resources {
				if val, ok := res[key]; ok {
					diff[names[i]] = val
				} else {
					diff[names[i]] = "<missing>"
				}
			}
			result[key] = diff
			continue
		}
		allSame := true
		for i := 1; i < len(values); i++ {
			if !reflect.DeepEqual(values[0], values[i]) {
				allSame = false
				break
			}
		}
		if allSame {
			result[key] = map[string]any{"_equal": values[0]}
			continue
		}
		allMaps := true
		nestedMaps := make([]map[string]any, len(resources))
		for i, val := range values {
			m, ok := val.(map[string]any)
			if !ok {
				allMaps = false
				break
			}
			nestedMaps[i] = m
		}
		if allMaps {
			nested := multiDiffMaps(names, nestedMaps, path+"."+key)
			if len(nested) > 0 {
				result[key] = nested
			}
			continue
		}
		allSlices := true
		slices := make([][]any, len(resources))
		for i, val := range values {
			s, ok := val.([]any)
			if !ok || !isStringSlice(s) {
				allSlices = false
				break
			}
			slices[i] = s
		}
		if allSlices {
			sliceDiff := multiDiffStringSlices(names, slices)
			result[key] = sliceDiff
			continue
		}
		diff := make(map[string]any)
		for i, val := range values {
			diff[names[i]] = val
		}
		result[key] = diff
	}
	return result
}

func multiDiffMetadata(names []string, resources []map[string]any) map[string]any {
	result := make(map[string]any)
	metas := make([]map[string]any, len(resources))
	for i, res := range resources {
		metas[i], _ = res["metadata"].(map[string]any)
	}
	labels := make([]map[string]any, len(resources))
	annots := make([]map[string]any, len(resources))
	for i, meta := range metas {
		if meta != nil {
			labels[i], _ = meta["labels"].(map[string]any)
			annots[i], _ = meta["annotations"].(map[string]any)
		}
	}
	if labelDiff := multiDiffMapFields(names, labels); len(labelDiff) > 0 {
		result["labels"] = labelDiff
	}
	if annotDiff := multiDiffMapFields(names, annots); len(annotDiff) > 0 {
		result["annotations"] = annotDiff
	}
	return result
}

func multiDiffMapFields(names []string, fields []map[string]any) map[string]any {
	result := make(map[string]any)
	allKeys := make(map[string]bool)
	for _, f := range fields {
		for k := range f {
			allKeys[k] = true
		}
	}
	for key := range allKeys {
		values := make(map[string]any)
		var firstVal any
		allSame := true
		allPresent := true
		for i, f := range fields {
			if val, ok := f[key]; ok {
				values[names[i]] = val
				if i == 0 {
					firstVal = val
				} else if !reflect.DeepEqual(firstVal, val) {
					allSame = false
				}
			} else {
				values[names[i]] = "<missing>"
				allPresent = false
				allSame = false
			}
		}
		if allSame && allPresent {
			result[key] = map[string]any{"_equal": firstVal}
		} else {
			result[key] = values
		}
	}
	return result
}

func multiDiffStringSlices(names []string, slices [][]any) map[string]any {
	counts := make(map[string]int)
	for _, slice := range slices {
		seen := make(map[string]bool)
		for _, v := range slice {
			s := v.(string)
			if !seen[s] {
				counts[s]++
				seen[s] = true
			}
		}
	}
	var common []string
	for s, count := range counts {
		if count == len(slices) {
			common = append(common, s)
		}
	}
	sort.Strings(common)
	result := make(map[string]any)
	if len(common) > 0 {
		result["_common"] = common
	}
	for i, slice := range slices {
		var unique []string
		for _, v := range slice {
			s := v.(string)
			if counts[s] != len(slices) {
				unique = append(unique, s)
			}
		}
		if len(unique) > 0 {
			sort.Strings(unique)
			result["onlyIn_"+names[i]] = unique
		}
	}
	return result
}

func isStringSlice(slice []any) bool {
	for _, v := range slice {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	return true
}
