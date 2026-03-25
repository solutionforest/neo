package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	sourceDir = envOr("NEO_SOURCE_DIR", "/src/neo")
	outputDir = envOr("NEO_OUTPUT_DIR", "/output")
	listenAddr = envOr("NEO_LISTEN_ADDR", ":9100")

	mu       sync.Mutex
	building bool

	versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[\w.]+)?$`)
)

type buildRequest struct {
	Version string `json:"version"`
}

type buildResponse struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Log     string `json:"log,omitempty"`
	Error   string `json:"error,omitempty"`
}

type platform struct {
	OS   string
	Arch string
	Name string
}

var platforms = []platform{
	{"darwin", "amd64", "neo-darwin-amd64"},
	{"darwin", "arm64", "neo-darwin-arm64"},
	{"linux", "amd64", "neo-linux-amd64"},
	{"linux", "arm64", "neo-linux-arm64"},
	{"windows", "amd64", "neo-windows-amd64.exe"},
}

func main() {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		log.Fatalf("cannot create output dir: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /build", handleBuild)
	mux.HandleFunc("GET /health", handleHealth)

	log.Printf("neo-builder listening on %s (source=%s, output=%s)", listenAddr, sourceDir, outputDir)
	log.Fatal(http.ListenAndServe(listenAddr, mux))
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	mu.Lock()
	busy := building
	mu.Unlock()

	status := "idle"
	if busy {
		status = "building"
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

func handleBuild(w http.ResponseWriter, r *http.Request) {
	var req buildRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, buildResponse{Error: "invalid JSON body"})
		return
	}

	if !versionRe.MatchString(req.Version) {
		writeJSON(w, http.StatusBadRequest, buildResponse{Error: "invalid version format, expected semver (e.g. 1.2.3)"})
		return
	}

	mu.Lock()
	if building {
		mu.Unlock()
		writeJSON(w, http.StatusConflict, buildResponse{Status: "busy", Error: "a build is already in progress"})
		return
	}
	building = true
	mu.Unlock()

	defer func() {
		mu.Lock()
		building = false
		mu.Unlock()
	}()

	resp := runBuild(req.Version)
	code := http.StatusOK
	if resp.Error != "" {
		code = http.StatusInternalServerError
	}
	writeJSON(w, code, resp)
}

func runBuild(version string) buildResponse {
	ldflags := fmt.Sprintf("-s -w -X main.version=%s", version)
	var logs strings.Builder

	// Create versioned output directory
	versionDir := filepath.Join(outputDir, version)
	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return buildResponse{
			Status:  "failed",
			Version: version,
			Error:   fmt.Sprintf("cannot create version dir: %v", err),
		}
	}

	// Download modules first
	modCmd := exec.Command("go", "mod", "download")
	modCmd.Dir = sourceDir
	modCmd.Env = buildEnv("linux", "amd64")
	if out, err := modCmd.CombinedOutput(); err != nil {
		return buildResponse{
			Status:  "failed",
			Version: version,
			Log:     string(out),
			Error:   fmt.Sprintf("go mod download failed: %v", err),
		}
	}

	start := time.Now()

	for _, p := range platforms {
		binary := filepath.Join(versionDir, p.Name)
		cmd := exec.Command("go", "build",
			"-ldflags", ldflags,
			"-o", binary,
			"./cmd/neo",
		)
		cmd.Dir = sourceDir
		cmd.Env = buildEnv(p.OS, p.Arch)

		out, err := cmd.CombinedOutput()
		logs.WriteString(fmt.Sprintf("=== %s-%s ===\n%s\n", p.OS, p.Arch, string(out)))

		if err != nil {
			logs.WriteString(fmt.Sprintf("FAILED: %v\n", err))
			return buildResponse{
				Status:  "failed",
				Version: version,
				Log:     logs.String(),
				Error:   fmt.Sprintf("build failed for %s-%s: %v", p.OS, p.Arch, err),
			}
		}

		logs.WriteString("OK\n\n")
	}

	duration := time.Since(start).Round(time.Millisecond)
	logs.WriteString(fmt.Sprintf("All platforms built in %s\n", duration))

	return buildResponse{
		Status:  "completed",
		Version: version,
		Log:     logs.String(),
	}
}

func buildEnv(goos, goarch string) []string {
	return append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+goos,
		"GOARCH="+goarch,
	)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
