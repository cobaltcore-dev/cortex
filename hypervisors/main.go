// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cobaltcore-dev/cortex/reservations/library"
	"github.com/sapcc/go-bits/must"
)

func main() {
	fmt.Println("Starting hypervisors client demo...")
	client := library.NewHypervisorsClient()
	client.Init()
	for {
		hypervisors := must.Return(client.ListHypervisors(context.Background()))
		fmt.Printf("Found %d hypervisors\n", len(hypervisors))
		time.Sleep(10 * time.Second)
	}
}
