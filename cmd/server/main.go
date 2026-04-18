package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/yourorg/finplan/engine"
)

func main() {
	addr      := flag.String("addr", ":8080", "Listen address")
	staticDir := flag.String("static", "cmd/server/static", "Static files directory")
	flag.Parse()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/plan",             withCORS(handlePlan))
	mux.HandleFunc("/api/monte-carlo",      withCORS(handleMonteCarlo))
	mux.HandleFunc("/api/projection",       withCORS(handleProjection))
	mux.HandleFunc("/api/cashflow",         withCORS(handleCashflow))
	mux.HandleFunc("/api/default-scenario", withCORS(handleDefaultScenario))
	mux.Handle("/", http.FileServer(http.Dir(*staticDir)))

	log.Printf("FinPlan server → http://localhost%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}

// withCORS wraps a handler with CORS headers for local development.
func withCORS(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h(w, r)
	}
}

func handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var params engine.PlanParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	result := engine.RunPlan(params)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleMonteCarlo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Params engine.PlanParams `json:"params"`
		Runs   int               `json:"runs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Runs <= 0 {
		req.Runs = 300
	}
	result := engine.RunMonteCarlo(req.Params, req.Runs, engine.DefaultReturnAssumptions())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleProjection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var params engine.PlanParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	result := engine.RunProjection(params)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleCashflow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var params engine.PlanParams
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	result := engine.RunCashflow(params)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func handleDefaultScenario(w http.ResponseWriter, r *http.Request) {
	data, err := os.ReadFile("scenarios/couple_sample.json")
	if err != nil {
		http.Error(w, "scenario not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}
