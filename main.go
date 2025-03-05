// Copyright 2013 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	// Define the address and directory to serve
	addr := flag.String("addr", ":8080", "http service address")
	dir := flag.String("dir", ".", "the directory to serve files from")
    hub := newHub()
    go hub.run()
	flag.Parse()

	// Create a file server to serve files from the specified directory
	fileServer := http.FileServer(http.Dir(*dir))

	// Handle "/" to serve all files in the directory
	http.Handle("/", fileServer)

    // Handle "/ws" for WebSocket connections
    http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
        serveWs(hub, w, r)
    })

	// Start the HTTP server
	log.Printf("Serving files from %s on HTTP port: %s\n", *dir, *addr)
	err := http.ListenAndServe(*addr, nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
