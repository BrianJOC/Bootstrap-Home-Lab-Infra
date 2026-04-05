package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

type CFResponse struct {
	Success  bool          `json:"success"`
	Errors   []interface{} `json:"errors"`
	Messages []interface{} `json:"messages"`
	Result   interface{}   `json:"result"`
}

func respond(w http.ResponseWriter, status int, result interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(CFResponse{
		Success:  status < 400,
		Errors:   []interface{}{},
		Messages: []interface{}{},
		Result:   result,
	})
}

func main() {
	port := os.Getenv("MOCK_CF_PORT")
	if port == "" {
		port = "8080"
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Never log Authorization header value
		log.Printf("%s %s", r.Method, r.URL.Path)

		if os.Getenv("MOCK_CF_RETURN_AUTH_ERROR") == "true" {
			respond(w, 403, nil)
			return
		}
		if os.Getenv("MOCK_CF_RETURN_RATE_LIMIT") == "true" {
			w.Header().Set("X-RateLimit-Reset", "1800000000")
			respond(w, 429, nil)
			return
		}

		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/zones"):
			respond(w, 200, []map[string]string{{"id": "fake-zone-id-12345", "name": "test"}})
		case r.Method == "POST" && strings.Contains(r.URL.Path, "/dns_records"):
			respond(w, 200, map[string]string{"id": "fake-record-id-67890"})
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/dns_records"):
			respond(w, 200, map[string]string{"id": "fake-record-id-67890"})
		// httpreq DNS provider endpoints (lego --dns=httpreq)
		case r.Method == "POST" && (r.URL.Path == "/present" || r.URL.Path == "/cleanup"):
			respond(w, 200, map[string]string{})
		default:
			log.Printf("UNEXPECTED request: %s %s — returning 404", r.Method, r.URL.Path)
			respond(w, 404, nil)
		}
	})

	fmt.Printf("Cloudflare API mock listening on :%s\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}
