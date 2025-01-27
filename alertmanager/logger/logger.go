// Copyright 2025 SAP SE
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
)

func handler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading request body: %v", err)
		http.Error(w, "can't read body", http.StatusBadRequest)
		return
	}
	log.Printf("Received alert: %s", body)
	fmt.Fprintf(w, "Alert received")
}

func main() {
	http.HandleFunc("/", handler)
	log.Println("Starting server on :9094")
	log.Fatal(http.ListenAndServe(":9094", nil))
}
