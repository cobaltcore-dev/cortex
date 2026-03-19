// Copyright SAP SE
// SPDX-License-Identifier: Apache-2.0

package commitments

import "time"

// Config defines the configuration for the commitments HTTP API.
type Config struct {
	// how long to wait for reservations to become ready before timing out and rolling back.
	ChangeAPIWatchReservationsTimeout time.Duration `json:"changeAPIWatchReservationsTimeout"`

	// how frequently to poll reservation status during watch.
	ChangeAPIWatchReservationsPollInterval time.Duration `json:"changeAPIWatchReservationsPollInterval"`
}

func DefaultConfig() Config {
	return Config{
		ChangeAPIWatchReservationsTimeout:      2 * time.Second,
		ChangeAPIWatchReservationsPollInterval: 100 * time.Millisecond,
	}
}
