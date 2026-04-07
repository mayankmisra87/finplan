package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/yourorg/finplan/engine"
)

//go:embed static/index.html
var indexHTML []byte

func main() {
	port := "8080"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/plan", withCORS(handlePlan))
	mux.HandleFunc("/api/monte-carlo", withCORS(handleMonteCarlo))
	mux.HandleFunc("/api/health", withCORS(handleHealth))
	mux.HandleFunc("/", serveUI)

	fmt.Printf("\n  finplan dev server\n  → http://localhost:%s\n\n", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	var params engine.PlanParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	result := engine.RunPlan(params)
	writeJSON(w, 200, map[string]interface{}{"ok": true, "data": result})
}

func handleMonteCarlo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]string{"error": "POST only"})
		return
	}
	runs := 300
	if s := r.URL.Query().Get("runs"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			runs = n
		}
	}
	var params engine.PlanParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	assumptions := engine.DefaultReturnAssumptions()
	result := engine.RunMonteCarlo(params, runs, assumptions)
	writeJSON(w, 200, map[string]interface{}{"ok": true, "data": result})
}

func withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func serveUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(indexHTML)
}
