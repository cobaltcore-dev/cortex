// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package compute

import (
	"fmt"
	"math"
	"time"
)

// Timestamp layouts returned by the openstack apis, with and without zone.
var timestampLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
}

// Parse an openstack timestamp. Timestamps without zone are treated as UTC.
func parseTimestamp(s string) (time.Time, error) {
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid timestamp %q", s)
}

// Number of whole seconds between two timestamps, rounded to the nearest second.
func secondsBetween(from, to time.Time) int {
	return int(math.Round(to.Sub(from).Seconds()))
}
