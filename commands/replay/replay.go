// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/sapcc/go-bits/jobloop"
	"github.com/sapcc/go-bits/must"
)

// Replay messages retrieved from the telemetry mqtt broker to a local Cortex instance.
// Use together with something like: `kubectl port-forward cortex-mqtt-0 18830:1883`
func main() {
	// Parse command-line arguments
	source := flag.String("h", "tcp://localhost:18830", "The cortex MQTT broker to connect to")
	username := flag.String("u", "cortex", "The username to use for the MQTT connection")
	password := flag.String("p", "secret", "The password to use for the MQTT connection")
	topic := flag.String("t", "cortex/scheduler/nova/pipeline/finished", "The topic to subscribe to")
	sink := flag.String("s", "", "The http endpoint to forward to")
	help := flag.Bool("help", false, "Show this help message")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [options]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()
	if *help {
		flag.Usage()
		os.Exit(0)
	}
	if *sink == "" {
		fmt.Fprintln(os.Stderr, "Error: The -s option is required to specify the HTTP endpoint to forward messages to.")
		flag.Usage()
		os.Exit(1)
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(*source)
	opts.SetUsername(*username)
	opts.SetPassword(*password)
	//nolint:gosec // We don't care if the client id is cryptographically secure.
	opts.SetClientID(fmt.Sprintf("cortex-replay-%d", rand.Intn(1_000_000)))

	client := mqtt.NewClient(opts)
	if conn := client.Connect(); conn.Wait() && conn.Error() != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to MQTT broker: %v\n", conn.Error())
		os.Exit(1)
	}
	defer client.Disconnect(1000)

	client.Subscribe(*topic, 2, func(client mqtt.Client, msg mqtt.Message) {
		// Unwrap the "request" from the message
		var payload map[string]any
		must.Succeed(json.Unmarshal(msg.Payload(), &payload))
		request, ok := payload["request"]
		if !ok {
			fmt.Fprintf(os.Stderr, "Message does not contain a 'request' field\n")
			return
		}
		for {
			// Forward the request to the local Cortex instance
			requestBody := must.Return(json.Marshal(request))
			req := must.Return(http.NewRequestWithContext(context.Background(), http.MethodPost, *sink, bytes.NewBuffer(requestBody)))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to forward message to Cortex: %v, retrying...\n", err)
				time.Sleep(jobloop.DefaultJitter(time.Second)) // wait before retrying
				continue
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				body := must.Return(io.ReadAll(resp.Body))
				fmt.Fprintf(os.Stderr, "Cortex responded with status %d: %s\n", resp.StatusCode, string(body))
				return
			}
			break
		}
		fmt.Printf("Successfully forwarded message received on topic %s to Cortex.\n", msg.Topic())
	})

	// Block the main thread to keep the program running
	select {}
}
