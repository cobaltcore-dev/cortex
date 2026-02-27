// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

// Package main provides a log parser for Nova scheduling logs.
// Usage: k logs deploy/cortex-nova-scheduling-controller-manager -f | go run tools/logs/parser.go
package main

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorPurple = "\033[35m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
	colorBold   = "\033[1m"
)

// hostColor returns ANSI codes for a unique background color with readable text
func hostColor(host string) string {
	// Simple hash function
	hash := uint32(0)
	for _, c := range host {
		hash = hash*31 + uint32(c)
	}

	// Use a curated set of distinguishable background colors with appropriate text colors
	// Format: {background color code, text color code (black=0 or white=15)}
	colorPairs := [][2]int{
		{196, 15}, // Red bg, white text
		{202, 0},  // Orange bg, black text
		{226, 0},  // Yellow bg, black text
		{46, 0},   // Green bg, black text
		{51, 0},   // Cyan bg, black text
		{21, 15},  // Blue bg, white text
		{201, 0},  // Magenta bg, black text
		{213, 0},  // Pink bg, black text
		{118, 0},  // Light green bg, black text
		{45, 0},   // Light cyan bg, black text
		{99, 15},  // Purple bg, white text
		{208, 0},  // Light orange bg, black text
		{190, 0},  // Lime bg, black text
		{159, 0},  // Light blue bg, black text
		{219, 0},  // Light pink bg, black text
		{123, 0},  // Aqua bg, black text
		{220, 0},  // Gold bg, black text
		{171, 15}, // Violet bg, white text
		{82, 0},   // Bright green bg, black text
		{39, 15},  // Deep sky blue bg, white text
		{197, 15}, // Deep pink bg, white text
		{214, 0},  // Orange yellow bg, black text
		{156, 0},  // Light lime bg, black text
		{87, 0},   // Dark cyan bg, black text
		{141, 15}, // Medium purple bg, white text
	}

	idx := hash % uint32(len(colorPairs)) //nolint:gosec // Safe: len(colorPairs) is a small constant
	bgColor := colorPairs[idx][0]
	fgColor := colorPairs[idx][1]

	// 48;5;X sets background color, 38;5;X sets foreground color
	return fmt.Sprintf("\033[48;5;%dm\033[38;5;%dm", bgColor, fgColor)
}

// colorizeHost returns the host name with its unique background color and readable text
func colorizeHost(host string) string {
	return hostColor(host) + " " + host + " " + colorReset
}

// colorizeHostList returns a list of hosts with each host uniquely colored
// If the list is long, it formats them in a multi-line layout
func colorizeHostList(hosts []string, indent int) string {
	if len(hosts) == 0 {
		return "[]"
	}

	// For short lists (<=5 hosts), keep them on one line
	if len(hosts) <= 5 {
		colored := make([]string, len(hosts))
		for i, h := range hosts {
			colored[i] = colorizeHost(h)
		}
		return "[" + strings.Join(colored, ", ") + "]"
	}

	// For longer lists, use multi-line format with 4 hosts per line
	var sb strings.Builder
	sb.WriteString("[\n")
	indentStr := strings.Repeat(" ", indent+2)

	for i, h := range hosts {
		if i%4 == 0 {
			sb.WriteString(indentStr)
		}
		sb.WriteString(colorizeHost(h))
		if i < len(hosts)-1 {
			sb.WriteString(", ")
		}
		if (i+1)%4 == 0 && i < len(hosts)-1 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n" + strings.Repeat(" ", indent) + "]")
	return sb.String()
}

// RequestState holds the state for a single scheduling request
type RequestState struct {
	ID              string
	FlavorName      string
	Pipeline        string
	InputHosts      []string
	FilterResults   map[string][]string
	WeigherResults  map[string]map[string]float64
	FinalSortedHost []string
	FilteredOut     []string
	FilterOrder     []string
	WeigherOrder    []string
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase buffer size for long log lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	var currentRequest *RequestState

	for scanner.Scan() {
		line := scanner.Text()
		processLine(line, &currentRequest)
	}

	// Print the last request if exists
	if currentRequest != nil {
		printRequest(currentRequest)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
		os.Exit(1)
	}
}

func processLine(line string, currentRequest **RequestState) {
	// Check for new request
	if strings.Contains(line, "handling POST request") && strings.Contains(line, "/scheduler/nova/external") {
		// Print previous request if exists
		if *currentRequest != nil {
			printRequest(*currentRequest)
		}

		*currentRequest = &RequestState{
			FilterResults:  make(map[string][]string),
			WeigherResults: make(map[string]map[string]float64),
		}

		// Extract instance UUID from spec
		if match := regexp.MustCompile(`InstanceUUID:([a-f0-9-]+)`).FindStringSubmatch(line); len(match) > 1 {
			(*currentRequest).ID = match[1]
		}
		// Extract flavor name - look for Flavor:{...Data:{...Name:FLAVOR_NAME...}
		if match := regexp.MustCompile(`Flavor:\{Name:Flavor[^}]*Data:\{[^}]*Name:([^\s}]+)`).FindStringSubmatch(line); len(match) > 1 {
			(*currentRequest).FlavorName = match[1]
		}
		return
	}

	if *currentRequest == nil {
		return
	}

	req := *currentRequest

	// Inferred pipeline
	if strings.Contains(line, "inferred pipeline name") {
		if match := regexp.MustCompile(`pipeline=([^\s]+)`).FindStringSubmatch(line); len(match) > 1 {
			req.Pipeline = match[1]
		}
		return
	}

	// Starting pipeline with input hosts
	if strings.Contains(line, "scheduler: starting pipeline") {
		// Try quoted format first, then unquoted
		if match := regexp.MustCompile(`hosts="\[([^\]]*)\]"`).FindStringSubmatch(line); len(match) > 1 {
			req.InputHosts = parseHostList(match[1])
		} else if match := regexp.MustCompile(`hosts=\[([^\]]*)\]`).FindStringSubmatch(line); len(match) > 1 {
			req.InputHosts = parseHostList(match[1])
		}
		return
	}

	// Finished step (filter or weigher) - capture output
	if strings.Contains(line, "scheduler: finished step") {
		name := extractField(line, "name")
		outWeightsStr := extractField(line, "outWeights")

		// Check if this is a filter or weigher by looking for the field type
		isFilter := strings.Contains(line, " filter=")
		isWeigher := strings.Contains(line, " weigher=")

		if name != "" && outWeightsStr != "" {
			hosts := parseWeightMap(outWeightsStr)
			hostNames := make([]string, 0, len(hosts))
			for h := range hosts {
				hostNames = append(hostNames, h)
			}
			sort.Strings(hostNames)

			if isFilter {
				if _, exists := req.FilterResults[name]; !exists {
					req.FilterOrder = append(req.FilterOrder, name)
				}
				req.FilterResults[name] = hostNames
			} else if isWeigher {
				if _, exists := req.WeigherResults[name]; !exists {
					req.WeigherOrder = append(req.WeigherOrder, name)
				}
				req.WeigherResults[name] = hosts
			}
		}
		return
	}

	// Sorted hosts (final output)
	if strings.Contains(line, "scheduler: sorted hosts") {
		// Try quoted format first, then unquoted
		if match := regexp.MustCompile(`hosts="\[([^\]]*)\]"`).FindStringSubmatch(line); len(match) > 1 {
			req.FinalSortedHost = parseHostList(match[1])
		} else if match := regexp.MustCompile(`hosts=\[([^\]]*)\]`).FindStringSubmatch(line); len(match) > 1 {
			req.FinalSortedHost = parseHostList(match[1])
		}
		return
	}

	// Filtered out hosts
	if strings.Contains(line, "filtering out host from response since it wasn't in the request") {
		if match := regexp.MustCompile(`host=([^\s]+)`).FindStringSubmatch(line); len(match) > 1 {
			req.FilteredOut = append(req.FilteredOut, match[1])
		}
		return
	}
}

func printRequest(req *RequestState) {
	fmt.Println()
	fmt.Printf("%s%s========================================%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%s%sNew Nova request with id: %s%s\n", colorBold, colorGreen, req.ID, colorReset)
	fmt.Printf("%s%s========================================%s\n", colorBold, colorCyan, colorReset)

	// Calculate maximum label width for alignment
	maxLabelWidth := 0
	labels := []string{"Inferred Pipeline", "Input hosts"}
	for _, name := range req.FilterOrder {
		labels = append(labels, "Filter "+name)
	}
	for _, name := range req.WeigherOrder {
		labels = append(labels, "Weigher "+name)
	}
	labels = append(labels, "Output of pipeline", "Removing unexpected hosts", "Final output")

	for _, label := range labels {
		if len(label) > maxLabelWidth {
			maxLabelWidth = len(label)
		}
	}

	if req.FlavorName != "" {
		fmt.Printf("%s%-*s:%s %s\n", colorYellow, maxLabelWidth, "Flavor", colorReset, req.FlavorName)
	}

	if req.Pipeline != "" {
		fmt.Printf("%s%-*s:%s %s\n", colorYellow, maxLabelWidth, "Inferred Pipeline", colorReset, req.Pipeline)
	}

	// Calculate indent for multi-line host lists (label width + ": " = maxLabelWidth + 2)
	indent := maxLabelWidth + 2

	if len(req.InputHosts) > 0 {
		sortedInputHosts := make([]string, len(req.InputHosts))
		copy(sortedInputHosts, req.InputHosts)
		sort.Strings(sortedInputHosts)
		fmt.Printf("%s%-*s:%s %s\n", colorYellow, maxLabelWidth, "Input hosts", colorReset, colorizeHostList(sortedInputHosts, indent))
	}

	// Print filter results in order
	for _, filterName := range req.FilterOrder {
		hosts := req.FilterResults[filterName]
		label := "Filter " + filterName
		fmt.Printf("%s%-*s:%s %s (%d hosts)\n",
			colorBlue, maxLabelWidth, label, colorReset, colorizeHostList(hosts, indent), len(hosts))
	}

	// Print weigher results in order
	for _, weigherName := range req.WeigherOrder {
		weights := req.WeigherResults[weigherName]
		label := "Weigher " + weigherName
		fmt.Printf("%s%-*s:%s %s\n",
			colorPurple, maxLabelWidth, label, colorReset, formatWeightMapFullColorized(weights, indent))
	}

	if len(req.FinalSortedHost) > 0 {
		fmt.Printf("%s%-*s:%s %s\n", colorGreen, maxLabelWidth, "Output of pipeline", colorReset, colorizeHostList(req.FinalSortedHost, indent))
	}

	if len(req.FilteredOut) > 0 {
		// Deduplicate filtered out hosts
		seen := make(map[string]bool)
		unique := make([]string, 0)
		for _, h := range req.FilteredOut {
			if !seen[h] {
				seen[h] = true
				unique = append(unique, h)
			}
		}
		sort.Strings(unique)
		fmt.Printf("%s%-*s:%s %s\n", colorRed, maxLabelWidth, "Removing unexpected hosts", colorReset, colorizeHostList(unique, indent))
	}

	// Calculate final output (sorted hosts minus filtered out)
	if len(req.FinalSortedHost) > 0 && len(req.FilteredOut) > 0 {
		filteredSet := make(map[string]bool)
		for _, h := range req.FilteredOut {
			filteredSet[h] = true
		}
		finalOutput := make([]string, 0)
		for _, h := range req.FinalSortedHost {
			if !filteredSet[h] {
				finalOutput = append(finalOutput, h)
			}
		}
		fmt.Printf("%s%s%-*s:%s %s\n", colorBold, colorGreen, maxLabelWidth, "Final output", colorReset, colorizeHostList(finalOutput, indent))
	} else if len(req.FinalSortedHost) > 0 {
		fmt.Printf("%s%s%-*s:%s %s\n", colorBold, colorGreen, maxLabelWidth, "Final output", colorReset, colorizeHostList(req.FinalSortedHost, indent))
	}

	fmt.Println()
}

func extractField(line, field string) string {
	// Try both quoted and unquoted patterns
	patterns := []string{
		field + `="([^"]*)"`,
		field + `=([^\s]+)`,
	}

	for _, pattern := range patterns {
		if match := regexp.MustCompile(pattern).FindStringSubmatch(line); len(match) > 1 {
			return match[1]
		}
	}
	return ""
}

func parseHostList(s string) []string {
	if s == "" {
		return nil
	}
	hosts := strings.Split(s, " ")
	result := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h != "" {
			result = append(result, h)
		}
	}
	return result
}

func parseWeightMap(s string) map[string]float64 {
	result := make(map[string]float64)

	// Remove "map[" prefix and "]" suffix
	s = strings.TrimPrefix(s, "map[")
	s = strings.TrimSuffix(s, "]")

	if s == "" {
		return result
	}

	// Split by space to get individual host:weight pairs
	pairs := strings.Split(s, " ")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, ":", 2)
		if len(parts) == 2 {
			weight, err := strconv.ParseFloat(parts[1], 64)
			if err != nil {
				weight = 0
			}
			result[parts[0]] = weight
		}
	}

	return result
}

func formatWeightMapFullColorized(weights map[string]float64, indent int) string {
	// Sort hosts by weight (descending)
	type hostWeight struct {
		host   string
		weight float64
	}
	sorted := make([]hostWeight, 0, len(weights))
	for h, w := range weights {
		sorted = append(sorted, hostWeight{h, w})
	}
	// Sort by weight (descending), then by hostname (ascending) for stable output
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].weight != sorted[j].weight {
			return sorted[i].weight > sorted[j].weight
		}
		return sorted[i].host < sorted[j].host
	})

	if len(sorted) == 0 {
		return "[]"
	}

	// For short lists (<=5 hosts), keep them on one line
	if len(sorted) <= 5 {
		parts := make([]string, 0, len(sorted))
		for _, hw := range sorted {
			parts = append(parts, fmt.Sprintf("%s: %.4f", colorizeHost(hw.host), hw.weight))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}

	// For longer lists, use multi-line format with 4 hosts per line
	var sb strings.Builder
	sb.WriteString("[\n")
	indentStr := strings.Repeat(" ", indent+2)

	for i, hw := range sorted {
		if i%4 == 0 {
			sb.WriteString(indentStr)
		}
		sb.WriteString(fmt.Sprintf("%s: %.4f", colorizeHost(hw.host), hw.weight))
		if i < len(sorted)-1 {
			sb.WriteString(", ")
		}
		if (i+1)%4 == 0 && i < len(sorted)-1 {
			sb.WriteString("\n")
		}
	}
	sb.WriteString("\n" + strings.Repeat(" ", indent) + "]")
	return sb.String()
}
