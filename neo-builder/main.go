package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	sourceDir  = envOr("NEO_SOURCE_DIR", "/src/neo")
	outputDir  = envOr("NEO_OUTPUT_DIR", "/output")
	listenAddr = envOr("NEO_LISTEN_ADDR", ":9100")

	// Staging endpoints — injected when version contains "-staging"
	stagingLicenseURL  = envOr("NEO_STAGING_LICENSE_URL", "https://neo-staging.vxero.dev/api/license")
	stagingAPIBaseURL  = envOr("NEO_STAGING_API_BASE_URL", "https://neo-staging.vxero.dev/api")
	stagingInstallURL  = envOr("NEO_STAGING_INSTALL_URL", "https://neo-staging.vxero.dev/neo")

	mu       sync.Mutex
	building bool

	versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(-[\w.]+)?$`)
)

type buildRequest struct {
	Version string `json:"version"`
}

type buildResponse struct {
	Status    string            `json:"status"`
	Version   string            `json:"version,omitempty"`
	Log       string            `json:"log,omitempty"`
	Error     string            `json:"error,omitempty"`
	Checksums map[string]string `json:"checksums,omitempty"` // "darwin-arm64" → "sha256:..."
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
	{"windows", "arm64", "neo-windows-arm64.exe"},
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
	if strings.Contains(version, "-staging") {
		ldflags += fmt.Sprintf(
			" -X github.com/vxero/neo/internal/license.DefaultLicenseAPIURL=%s"+
				" -X github.com/vxero/neo/internal/config.DefaultAPIBaseURL=%s"+
				" -X github.com/vxero/neo/internal/config.DefaultInstallURL=%s",
			stagingLicenseURL, stagingAPIBaseURL, stagingInstallURL,
		)
	}
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
	checksums := make(map[string]string)

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

		// Compute SHA256 for this binary
		data, err := os.ReadFile(binary)
		if err != nil {
			return buildResponse{
				Status:  "failed",
				Version: version,
				Log:     logs.String(),
				Error:   fmt.Sprintf("failed to read binary for checksum %s-%s: %v", p.OS, p.Arch, err),
			}
		}
		sum := sha256.Sum256(data)
		checksums[fmt.Sprintf("%s-%s", p.OS, p.Arch)] = "sha256:" + hex.EncodeToString(sum[:])

		logs.WriteString("OK\n\n")
	}

	duration := time.Since(start).Round(time.Millisecond)
	logs.WriteString(fmt.Sprintf("All platforms built in %s\n", duration))

	// Retain only the most recent N versions per channel (staging vs prod) so
	// old builds don't accumulate on disk. The version just built is the newest,
	// so it is always kept. Best-effort: a prune failure never fails the build.
	keep := envIntOr("NEO_KEEP_VERSIONS", 3)
	if removed, err := pruneOldVersions(outputDir, keep); err != nil {
		logs.WriteString(fmt.Sprintf("version prune skipped: %v\n", err))
	} else if len(removed) > 0 {
		logs.WriteString(fmt.Sprintf("pruned %d old version(s), keeping %d per channel: %s\n",
			len(removed), keep, strings.Join(removed, ", ")))
	}

	return buildResponse{
		Status:    "completed",
		Version:   version,
		Log:       logs.String(),
		Checksums: checksums,
	}
}

// pruneOldVersions keeps only the newest `keep` version directories per channel
// (staging versions contain "-staging", everything else is treated as prod) and
// removes the rest. Ordering is by directory mtime (build recency) descending.
// Only directories whose name is a valid version are considered, so unrelated
// files in the output dir are never touched. Returns the removed version names.
func pruneOldVersions(dir string, keep int) ([]string, error) {
	if keep < 1 {
		keep = 1
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type verDir struct {
		name string
		mod  time.Time
	}
	var staging, prod []verDir
	for _, e := range entries {
		if !e.IsDir() || !versionRe.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		vd := verDir{name: e.Name(), mod: info.ModTime()}
		if strings.Contains(e.Name(), "-staging") {
			staging = append(staging, vd)
		} else {
			prod = append(prod, vd)
		}
	}

	var removed []string
	prune := func(list []verDir) {
		sort.Slice(list, func(i, j int) bool { return list[i].mod.After(list[j].mod) })
		for i := keep; i < len(list); i++ {
			if err := os.RemoveAll(filepath.Join(dir, list[i].name)); err == nil {
				removed = append(removed, list[i].name)
			}
		}
	}
	prune(staging)
	prune(prod)
	return removed, nil
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

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
