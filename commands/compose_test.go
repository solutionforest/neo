package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseComposeFileMapEnv(t *testing.T) {
	content := `services:
  app:
    build: .
    ports:
      - "8080:3000"
    environment:
      APP_ENV: production
      DB_HOST: mysql
      DB_PORT: 3306
  mysql:
    image: mysql:8
    environment:
      MYSQL_ROOT_PASSWORD: secret
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "docker-compose.yml")
	os.WriteFile(path, []byte(content), 0644)

	result, err := parseComposeFile(path, "app")
	if err != nil {
		t.Fatalf("parseComposeFile() error: %v", err)
	}

	if result.ServiceName != "app" {
		t.Errorf("ServiceName = %q, want %q", result.ServiceName, "app")
	}
	if result.Env["APP_ENV"] != "production" {
		t.Errorf("APP_ENV = %q", result.Env["APP_ENV"])
	}
	if result.Env["DB_HOST"] != "mysql" {
		t.Errorf("DB_HOST = %q", result.Env["DB_HOST"])
	}
	if result.Env["DB_PORT"] != "3306" {
		t.Errorf("DB_PORT = %q", result.Env["DB_PORT"])
	}
	if result.Port != 3000 {
		t.Errorf("Port = %d, want 3000", result.Port)
	}
	// Should NOT include mysql service env vars
	if _, ok := result.Env["MYSQL_ROOT_PASSWORD"]; ok {
		t.Error("should not include env from other services")
	}
}

func TestParseComposeFileListEnv(t *testing.T) {
	content := `services:
  web:
    build: .
    environment:
      - APP_ENV=production
      - DB_HOST=localhost
      - APP_KEY=base64:abc=123
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "docker-compose.yml")
	os.WriteFile(path, []byte(content), 0644)

	result, err := parseComposeFile(path, "")
	if err != nil {
		t.Fatalf("parseComposeFile() error: %v", err)
	}

	if result.Env["APP_ENV"] != "production" {
		t.Errorf("APP_ENV = %q", result.Env["APP_ENV"])
	}
	if result.Env["APP_KEY"] != "base64:abc=123" {
		t.Errorf("APP_KEY = %q, want %q", result.Env["APP_KEY"], "base64:abc=123")
	}
}

func TestParseComposeFileLaravel(t *testing.T) {
	content := `services:
  app:
    build:
      context: .
      dockerfile: Dockerfile
    ports:
      - "8080:8000"
    environment:
      APP_NAME: Vanguard
      APP_ENV: production
      APP_KEY: "base64:dGhpcyBpcyBhIHRlc3Qga2V5"
      APP_DEBUG: false
      APP_URL: https://vanguard.dev
      DB_CONNECTION: mysql
      DB_HOST: mysql
      DB_PORT: 3306
      DB_DATABASE: vanguard
      DB_USERNAME: root
      DB_PASSWORD: secret
      REDIS_HOST: redis
      CACHE_DRIVER: redis
      SESSION_DRIVER: redis
    depends_on:
      - mysql
      - redis
  mysql:
    image: mysql:8.0
    environment:
      MYSQL_ROOT_PASSWORD: secret
      MYSQL_DATABASE: vanguard
    volumes:
      - mysql-data:/var/lib/mysql
  redis:
    image: redis:7-alpine
volumes:
  mysql-data:
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "docker-compose.yml")
	os.WriteFile(path, []byte(content), 0644)

	// Auto-detect the app service (has build, not infra)
	result, err := parseComposeFile(path, "")
	if err != nil {
		t.Fatalf("parseComposeFile() error: %v", err)
	}

	if result.ServiceName != "app" {
		t.Errorf("ServiceName = %q, want %q (should auto-detect build service)", result.ServiceName, "app")
	}
	if result.Env["APP_NAME"] != "Vanguard" {
		t.Errorf("APP_NAME = %q", result.Env["APP_NAME"])
	}
	if result.Env["DB_HOST"] != "mysql" {
		t.Errorf("DB_HOST = %q", result.Env["DB_HOST"])
	}
	if result.Port != 8000 {
		t.Errorf("Port = %d, want 8000", result.Port)
	}
	if len(result.Env) != 14 {
		t.Errorf("expected 14 env vars, got %d", len(result.Env))
	}
}

func TestParseComposeFileWithEnvFile(t *testing.T) {
	envContent := `DB_HOST=localhost
DB_PORT=3306
APP_SECRET=hunter2
`
	composeContent := `services:
  app:
    build: .
    env_file:
      - .env
    environment:
      APP_ENV: production
      DB_HOST: mysql
`
	tmp := t.TempDir()
	os.WriteFile(filepath.Join(tmp, ".env"), []byte(envContent), 0644)
	os.WriteFile(filepath.Join(tmp, "docker-compose.yml"), []byte(composeContent), 0644)

	result, err := parseComposeFile(filepath.Join(tmp, "docker-compose.yml"), "")
	if err != nil {
		t.Fatalf("parseComposeFile() error: %v", err)
	}

	// Explicit environment should override env_file
	if result.Env["DB_HOST"] != "mysql" {
		t.Errorf("DB_HOST = %q, want %q (explicit should override env_file)", result.Env["DB_HOST"], "mysql")
	}
	// env_file values should be present
	if result.Env["APP_SECRET"] != "hunter2" {
		t.Errorf("APP_SECRET = %q (from env_file)", result.Env["APP_SECRET"])
	}
}

func TestParseComposePortFormats(t *testing.T) {
	tests := []struct {
		ports []string
		want  int
	}{
		{[]string{"8080:3000"}, 3000},
		{[]string{"3000"}, 3000},
		{[]string{"8080:3000/tcp"}, 3000},
		{[]string{"0.0.0.0:8080:3000"}, 3000},
		{nil, 0},
		{[]string{}, 0},
	}

	for _, tt := range tests {
		got := parseComposePort(tt.ports)
		if got != tt.want {
			t.Errorf("parseComposePort(%v) = %d, want %d", tt.ports, got, tt.want)
		}
	}
}

func TestParseComposeEnvFileForms(t *testing.T) {
	// string form
	if got := parseComposeEnvFile(".env"); len(got) != 1 || got[0] != ".env" {
		t.Errorf("string form = %v, want [.env]", got)
	}

	// list-of-strings form
	if got := parseComposeEnvFile([]interface{}{".env", ".env.local"}); len(got) != 2 || got[1] != ".env.local" {
		t.Errorf("list-of-strings form = %v", got)
	}

	// long { path, required } form — must not stringify the whole map
	long := []interface{}{
		map[string]interface{}{"path": "./share.env", "required": true},
		map[string]interface{}{"path": "./override.env", "required": false},
	}
	got := parseComposeEnvFile(long)
	if len(got) != 2 || got[0] != "./share.env" || got[1] != "./override.env" {
		t.Errorf("long form = %v, want [./share.env ./override.env]", got)
	}

	if got := parseComposeEnvFile(nil); got != nil {
		t.Errorf("nil form = %v, want nil", got)
	}
}

func TestComposeBuildDockerfile(t *testing.T) {
	if got := composeBuildDockerfile(map[string]interface{}{"context": "./", "dockerfile": "Dockerfile.local"}); got != "Dockerfile.local" {
		t.Errorf("map form = %q, want Dockerfile.local", got)
	}
	if got := composeBuildDockerfile("./"); got != "" {
		t.Errorf("string (context-only) form = %q, want empty", got)
	}
	if got := composeBuildDockerfile(nil); got != "" {
		t.Errorf("nil form = %q, want empty", got)
	}
}

func TestComposeBindMounts(t *testing.T) {
	vols := []string{"../:/var/www/html", "./data/db:/var/lib/postgresql/data", "named-vol:/data"}
	got := composeBindMounts(vols)
	if len(got) != 2 {
		t.Fatalf("composeBindMounts() = %v, want 2 bind mounts", got)
	}
	if got := composeBindMounts([]string{"named-vol:/data"}); len(got) != 0 {
		t.Errorf("named volume flagged as bind: %v", got)
	}
}

func TestGuessAppService(t *testing.T) {
	services := map[string]composeService{
		"app":   {Build: ".", Ports: []string{"8080:3000"}},
		"mysql": {Image: "mysql:8"},
		"redis": {Image: "redis:7"},
	}

	name, _ := guessAppService(services)
	if name != "app" {
		t.Errorf("guessAppService() = %q, want %q", name, "app")
	}
}

func TestGuessAppServiceNoInfra(t *testing.T) {
	services := map[string]composeService{
		"web":      {Build: "."},
		"postgres": {Image: "postgres:15"},
		"nginx":    {Image: "nginx:latest"},
	}

	name, _ := guessAppService(services)
	if name != "web" {
		t.Errorf("guessAppService() = %q, want %q", name, "web")
	}
}

func TestFindComposeFile(t *testing.T) {
	tmp := t.TempDir()

	// No compose file
	if got := findComposeFile(tmp); got != "" {
		t.Errorf("expected empty, got %q", got)
	}

	// docker-compose.yml
	os.WriteFile(filepath.Join(tmp, "docker-compose.yml"), []byte("services:"), 0644)
	if got := findComposeFile(tmp); got == "" {
		t.Error("expected to find docker-compose.yml")
	}
}

func TestFindComposeFileAlternateNames(t *testing.T) {
	tmp := t.TempDir()

	// compose.yml (newer convention)
	os.WriteFile(filepath.Join(tmp, "compose.yml"), []byte("services:"), 0644)
	if got := findComposeFile(tmp); got == "" {
		t.Error("expected to find compose.yml")
	}
}

func TestParseComposeFileMissingService(t *testing.T) {
	content := `services:
  app:
    build: .
`
	tmp := t.TempDir()
	path := filepath.Join(tmp, "docker-compose.yml")
	os.WriteFile(path, []byte(content), 0644)

	_, err := parseComposeFile(path, "nonexistent")
	if err == nil {
		t.Error("expected error for missing service")
	}
}

// A compose file where every service shares one prebuilt image — a web tier
// plus queue workers and a scheduler. This shape used to pick a random service
// as the public app on each run.
func multiServiceOneImageCompose() map[string]composeService {
	return map[string]composeService{
		"admission-web": {
			Image:       "org/project:tag",
			Environment: []interface{}{"VIRTUAL_HOST=admission.example.com", "VIRTUAL_PORT=8080", "APP_PURPOSE=NOMINATION"},
		},
		"admission-queue": {
			Image:       "org/project:tag",
			Command:     []interface{}{"php", "artisan", "queue:work", "--queue=nomination"},
			Environment: []interface{}{"APP_PURPOSE=NOMINATION"},
		},
		"study-web": {
			Image:       "org/project:tag",
			Environment: []interface{}{"VIRTUAL_HOST=study.example.com", "VIRTUAL_PORT=8080", "APP_PURPOSE=STUDY"},
		},
		"task": {
			Image:   "org/project:tag",
			Command: []interface{}{"php", "artisan", "schedule:work"},
		},
		"redis": {Image: "redis:7-alpine"},
	}
}

func TestGuessAppServiceIsDeterministic(t *testing.T) {
	services := multiServiceOneImageCompose()

	first, _ := guessAppService(services)
	if first == "" {
		t.Fatal("no app service chosen")
	}
	for i := 0; i < 50; i++ {
		if got, _ := guessAppService(services); got != first {
			t.Fatalf("selection changed between runs: %q then %q", first, got)
		}
	}
}

func TestGuessAppServiceNeverPicksAWorker(t *testing.T) {
	services := multiServiceOneImageCompose()

	name, _ := guessAppService(services)
	if name == "admission-queue" || name == "task" {
		t.Errorf("picked background service %q as the public app", name)
	}
	// The web service with a VIRTUAL_HOST is the right answer; ties break by name.
	if name != "admission-web" {
		t.Errorf("app = %q, want admission-web", name)
	}
}

func TestGuessAppServicePrefersBuildOverVirtualHost(t *testing.T) {
	services := map[string]composeService{
		"proxied": {Image: "org/app:tag", Environment: []interface{}{"VIRTUAL_HOST=a.example.com"}},
		"built":   {Build: "./", Image: "org/app:tag"},
	}
	if name, _ := guessAppService(services); name != "built" {
		t.Errorf("app = %q, want built", name)
	}
}

func TestGuessAppServiceSkipsInfra(t *testing.T) {
	services := map[string]composeService{
		"redis": {Image: "redis:7-alpine", Ports: []string{"6379:6379"}},
		"db":    {Image: "mysql:8", Ports: []string{"3306:3306"}},
	}
	if name, _ := guessAppService(services); name != "" {
		t.Errorf("app = %q, want none — everything is infrastructure", name)
	}
}

func TestLooksLikeBackgroundCommand(t *testing.T) {
	background := []string{
		"php artisan queue:work --tries=3",
		"php /var/www/html/artisan schedule:work",
		"php artisan horizon",
		"supervisord -n",
	}
	for _, cmd := range background {
		if !looksLikeBackgroundCommand(cmd) {
			t.Errorf("looksLikeBackgroundCommand(%q) = false", cmd)
		}
	}

	foreground := []string{
		"php artisan octane:frankenphp",
		"nginx -g 'daemon off;'",
		"node server.js",
	}
	for _, cmd := range foreground {
		if looksLikeBackgroundCommand(cmd) {
			t.Errorf("looksLikeBackgroundCommand(%q) = true", cmd)
		}
	}
}

func TestComposePublicServices(t *testing.T) {
	got := composePublicServices(multiServiceOneImageCompose())
	want := []string{"admission-web", "study-web"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got %v, want %v", got, want)
		}
	}
}

func TestConflictingEnvKeys(t *testing.T) {
	app := map[string]string{"APP_PURPOSE": "NOMINATION", "DB_QUEUE": "nomination", "TZ": "Asia/Hong_Kong"}

	// A worker inherits the app's env, so a service needing different values
	// cannot be one — this is what keeps a study queue off the nomination queue.
	conflicts := conflictingEnvKeys(app, map[string]string{"APP_PURPOSE": "STUDY", "DB_QUEUE": "study"})
	if len(conflicts) != 2 || conflicts[0] != "APP_PURPOSE" || conflicts[1] != "DB_QUEUE" {
		t.Errorf("conflicts = %v, want [APP_PURPOSE DB_QUEUE]", conflicts)
	}

	// Same values, or keys the app doesn't set, are not conflicts.
	if got := conflictingEnvKeys(app, map[string]string{"APP_PURPOSE": "NOMINATION", "NEW_KEY": "x"}); len(got) != 0 {
		t.Errorf("conflicts = %v, want none", got)
	}
}

func TestSharesAppArtifact(t *testing.T) {
	app := composeService{Image: "org/project:tag"}

	if !sharesAppArtifact(app, composeService{Image: "org/project:tag"}) {
		t.Error("same image should share the artifact")
	}
	if sharesAppArtifact(app, composeService{Image: "redis:7"}) {
		t.Error("different image should not share the artifact")
	}
	if !sharesAppArtifact(composeService{Build: "./"}, composeService{Build: "./"}) {
		t.Error("same build context should share the artifact")
	}
	if sharesAppArtifact(composeService{Build: "./"}, composeService{Build: "./other"}) {
		t.Error("different build contexts should not share the artifact")
	}
}

func TestLooksLikeBackgroundCommandIgnoresFlags(t *testing.T) {
	// Octane/FrankenPHP take --workers=N. Matching "worker" inside that flag
	// disqualified the actual web service and handed the app role to whatever
	// sorted next.
	server := "--port=80 --workers=${OCTANE_WORKERS:-auto} --max-requests=500"
	if looksLikeBackgroundCommand(server) {
		t.Errorf("a server with --workers= was treated as a background process: %q", server)
	}
	if looksLikeBackgroundCommand("php artisan reverb:start --host=0.0.0.0") {
		t.Error("reverb:start is a server, not a background process")
	}
	// Real background work still matches.
	if !looksLikeBackgroundCommand("php artisan queue:work --tries=3") {
		t.Error("queue:work should be background")
	}
	if !looksLikeBackgroundCommand("node worker.js") {
		t.Error("a bare worker program should be background")
	}
}

func TestComposeFullCommand(t *testing.T) {
	// Compose routinely splits these; reading command alone gives "horizon",
	// which is not a program.
	svc := composeService{
		Entrypoint: []interface{}{"php", "artisan"},
		Command:    "horizon",
	}
	if got := composeFullCommand(svc); got != "php artisan horizon" {
		t.Errorf("got %q, want php artisan horizon", got)
	}

	if got := composeFullCommand(composeService{Command: "npm run build"}); got != "npm run build" {
		t.Errorf("command only: got %q", got)
	}
	if got := composeFullCommand(composeService{Entrypoint: []interface{}{"composer"}}); got != "composer" {
		t.Errorf("entrypoint only: got %q", got)
	}
	if got := composeFullCommand(composeService{}); got != "" {
		t.Errorf("neither: got %q", got)
	}
}

func TestIsOneShotService(t *testing.T) {
	if !isOneShotService(composeService{Restart: "no"}) {
		t.Error(`restart: "no" is a one-shot step`)
	}
	if !isOneShotService(composeService{Restart: "on-failure"}) {
		t.Error("on-failure is a one-shot step")
	}
	if isOneShotService(composeService{Restart: "unless-stopped"}) {
		t.Error("unless-stopped is long-running")
	}
	if isOneShotService(composeService{}) {
		t.Error("no restart policy should not be treated as one-shot")
	}
}

func TestGuessAppServiceSkipsOneShotAndPicksTheServer(t *testing.T) {
	// Shape of a modern Laravel compose: init steps, a server, and workers all
	// built from the same Dockerfile.
	services := map[string]composeService{
		"composer": {Build: "./", Entrypoint: []interface{}{"composer"}, Command: "install", Restart: "no"},
		"setup":    {Build: "./", Command: "php artisan migrate --force", Restart: "no"},
		"app": {
			Build:   "./",
			Ports:   []string{"${APP_PORT:-8080}:80"},
			Command: "--port=80 --workers=auto",
		},
		"horizon":   {Build: "./", Entrypoint: []interface{}{"php", "artisan"}, Command: "horizon", Restart: "unless-stopped"},
		"scheduler": {Build: "./", Entrypoint: []interface{}{"php", "artisan"}, Command: "schedule:work", Restart: "unless-stopped"},
		"postgres":  {Image: "postgres:15-alpine", Ports: []string{"5432:5432"}},
	}

	name, _ := guessAppService(services)
	if name != "app" {
		t.Errorf("app = %q, want app", name)
	}

	// And it stays that way.
	for i := 0; i < 25; i++ {
		if got, _ := guessAppService(services); got != "app" {
			t.Fatalf("selection changed to %q", got)
		}
	}

	// One-shot steps are not public services either.
	public := composePublicServices(services)
	for _, name := range public {
		if name == "composer" || name == "setup" {
			t.Errorf("one-shot service %q reported as publicly reachable", name)
		}
	}
}

func TestComposeBuildTargetAndArgs(t *testing.T) {
	build := map[string]interface{}{
		"context":    ".",
		"dockerfile": "Dockerfile",
		"target":     "development",
		"args":       map[string]interface{}{"USER_ID": "1000", "GROUP_ID": "1000"},
	}

	if got := composeBuildTarget(build); got != "development" {
		t.Errorf("target = %q", got)
	}
	args := composeBuildArgs(build)
	if len(args) != 2 || args[0] != "GROUP_ID" || args[1] != "USER_ID" {
		t.Errorf("args = %v, want [GROUP_ID USER_ID]", args)
	}

	// String build form carries neither.
	if got := composeBuildTarget("./"); got != "" {
		t.Errorf("string build target = %q", got)
	}
	if got := composeBuildArgs("./"); got != nil {
		t.Errorf("string build args = %v", got)
	}
}

func TestLooksLikeMigrationCommand(t *testing.T) {
	for _, cmd := range []string{
		"php artisan migrate --force",
		"php artisan key:generate",
		"php artisan storage:link",
	} {
		if !looksLikeMigrationCommand(cmd) {
			t.Errorf("looksLikeMigrationCommand(%q) = false", cmd)
		}
	}
	for _, cmd := range []string{"composer install", "npm run build"} {
		if looksLikeMigrationCommand(cmd) {
			t.Errorf("looksLikeMigrationCommand(%q) = true", cmd)
		}
	}
}

func TestUnmigratedComposeKeys(t *testing.T) {
	keys := []string{
		// handled
		"image", "build", "ports", "environment", "env_file", "volumes",
		"command", "entrypoint", "restart",
		// safely ignorable for a Neo deploy
		"container_name", "networks", "depends_on", "logging",
		// genuinely lost
		"healthcheck", "deploy", "labels", "user", "working_dir", "shm_size",
	}

	got := unmigratedComposeKeys(keys)
	want := []string{"healthcheck", "deploy", "labels", "user", "working_dir", "shm_size"}

	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	seen := map[string]bool{}
	for _, k := range got {
		seen[k] = true
	}
	for _, k := range want {
		if !seen[k] {
			t.Errorf("%q should have been reported as unmigrated", k)
		}
	}
}

func TestUnmigratedComposeKeysNothingToReport(t *testing.T) {
	if got := unmigratedComposeKeys([]string{"image", "ports", "container_name"}); len(got) != 0 {
		t.Errorf("got %v, want nothing", got)
	}
}

func TestParseComposeRawServicesSeesEveryKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-compose.yml")
	content := `services:
  app:
    image: app:latest
    healthcheck:
      test: ["CMD", "true"]
    deploy:
      replicas: 3
  db:
    image: postgres:15
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	raw, err := parseComposeRawServices(path)
	if err != nil {
		t.Fatalf("parseComposeRawServices: %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("got %d services, want 2", len(raw))
	}

	// The typed parser drops healthcheck and deploy; the raw pass must not, or
	// there is nothing to warn about.
	unmigrated := unmigratedComposeKeys(raw["app"])
	if len(unmigrated) != 2 {
		t.Errorf("app unmigrated = %v, want healthcheck and deploy", unmigrated)
	}
	if len(unmigratedComposeKeys(raw["db"])) != 0 {
		t.Errorf("db should have nothing unmigrated")
	}
}

func TestEveryUnmigratedKeyWithAdviceIsActuallyUnmigrated(t *testing.T) {
	// Advice for a key generate already handles would be misleading.
	for key := range composeKeyAdvice {
		if composeHandledKeys[key] {
			t.Errorf("%q has advice but is handled — the advice would be wrong", key)
		}
		if composeIgnorableKeys[key] {
			t.Errorf("%q has advice but is ignored — it would never be shown", key)
		}
	}
}
