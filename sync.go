package main

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// ─── Sync status handler ───────────────────────────────────────────────────────

type SyncStatus struct {
	Available bool   `json:"available"`
	State     string `json:"state"` // "synced", "syncing", "error", "unknown"
	Message   string `json:"message,omitempty"`
}

var syncHTTPClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	},
}

func (s *server) handleSyncStatus(w http.ResponseWriter, r *http.Request) {
	apiKey := os.Getenv("SYNCTHING_API_KEY")
	apiURL := os.Getenv("SYNCTHING_API_URL") // e.g. https://172.10.0.5:8384
	if apiKey == "" || apiURL == "" {
		jsonResponse(w, SyncStatus{Available: false, State: "unknown"})
		return
	}

	req, err := http.NewRequest("GET", apiURL+"/rest/db/completion", nil)
	if err != nil {
		jsonResponse(w, SyncStatus{Available: false, State: "error", Message: err.Error()})
		return
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := syncHTTPClient.Do(req)
	if err != nil {
		jsonResponse(w, SyncStatus{Available: false, State: "error", Message: "unreachable"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		jsonResponse(w, SyncStatus{Available: true, State: "error", Message: fmt.Sprintf("HTTP %d", resp.StatusCode)})
		return
	}

	var completion struct {
		Completion float64 `json:"completion"`
		NeedBytes  int64   `json:"needBytes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		jsonResponse(w, SyncStatus{Available: true, State: "error", Message: "parse error"})
		return
	}

	if completion.NeedBytes == 0 {
		jsonResponse(w, SyncStatus{Available: true, State: "synced", Message: "Up to date"})
	} else {
		jsonResponse(w, SyncStatus{Available: true, State: "syncing",
			Message: fmt.Sprintf("%.0f%%", completion.Completion)})
	}
}
