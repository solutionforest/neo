package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/laravel"
	"github.com/vxero/neo/internal/ui"
)

// Environment variables that supply the decryption key without a prompt.
// LARAVEL_ENV_ENCRYPTION_KEY is the name Laravel's own env:decrypt reads, so a
// CI job already exporting it needs no neo-specific setup.
const (
	envKeyVar        = "NEO_ENV_KEY"
	laravelEnvKeyVar = "LARAVEL_ENV_ENCRYPTION_KEY"
)

// envKeyStore is the local cache of environment encryption keys, kept in
// ~/.neo/keys.json (0600). Keys are stored in plain text — the file is a
// convenience so you don't retype the key on every deploy, not a vault.
type envKeyStore struct {
	Keys map[string]string `json:"keys"`
}

func envKeyStorePath() string {
	return filepath.Join(config.Dir(), "keys.json")
}

func loadEnvKeyStore() (*envKeyStore, error) {
	store := &envKeyStore{Keys: make(map[string]string)}

	data, err := os.ReadFile(envKeyStorePath())
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, fmt.Errorf("read %s: %w", envKeyStorePath(), err)
	}
	if err := json.Unmarshal(data, store); err != nil {
		return nil, fmt.Errorf("parse %s: %w", envKeyStorePath(), err)
	}
	if store.Keys == nil {
		store.Keys = make(map[string]string)
	}
	return store, nil
}

// saveEnvKeyStore writes the key store atomically with 0600 permissions.
func saveEnvKeyStore(store *envKeyStore) error {
	if err := os.MkdirAll(config.Dir(), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal keys: %w", err)
	}

	tmpPath := envKeyStorePath() + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("write keys: %w", err)
	}
	if err := os.Rename(tmpPath, envKeyStorePath()); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("save keys: %w", err)
	}
	return nil
}

func lookupEnvKey(appID string) string {
	store, err := loadEnvKeyStore()
	if err != nil {
		return ""
	}
	return store.Keys[appID]
}

func rememberEnvKey(appID, key string) error {
	store, err := loadEnvKeyStore()
	if err != nil {
		return err
	}
	store.Keys[appID] = key
	return saveEnvKeyStore(store)
}

func forgetEnvKey(appID string) (bool, error) {
	store, err := loadEnvKeyStore()
	if err != nil {
		return false, err
	}
	if _, ok := store.Keys[appID]; !ok {
		return false, nil
	}
	delete(store.Keys, appID)
	return true, saveEnvKeyStore(store)
}

// envKeySource records where a resolved key came from, so callers can react
// once they know whether it actually decrypted the file.
type envKeySource int

const (
	keyFromFlag envKeySource = iota
	keyFromEnvVar
	keyFromStore
	keyFromPrompt
)

// resolvedEnvKey is a key plus its provenance.
type resolvedEnvKey struct {
	key    string
	source envKeySource
	appID  string // identity the key would be saved under
}

// resolveEnvKey finds the decryption key for an encrypted env file.
// Priority: --env-key flag > NEO_ENV_KEY > LARAVEL_ENV_ENCRYPTION_KEY >
// ~/.neo/keys.json > interactive prompt.
//
// appIDs are tried in order against the key store; the first entry is the
// identity a prompted key is saved under. Deploy passes the environment-suffixed
// app name first and the bare project name second, so a key saved from the
// project root is still found when deploying `my-app-staging`.
//
// A prompted key is NOT saved here — the caller saves it only after it has
// actually decrypted the file, so a typo never gets cached.
//
// allowPrompt is false on code paths that run unattended or in parallel, where
// a prompt would corrupt the display.
func resolveEnvKey(appIDs []string, flagKey string, allowPrompt bool) (resolvedEnvKey, error) {
	ids := dedupeStrings(appIDs)
	if len(ids) == 0 {
		return resolvedEnvKey{}, fmt.Errorf("no app name to look up an encryption key for")
	}
	primary := ids[0]

	if flagKey != "" {
		key, err := validateEnvKey(flagKey)
		return resolvedEnvKey{key: key, source: keyFromFlag, appID: primary}, err
	}
	for _, name := range []string{envKeyVar, laravelEnvKeyVar} {
		if k := os.Getenv(name); k != "" {
			key, err := validateEnvKey(k)
			return resolvedEnvKey{key: key, source: keyFromEnvVar, appID: primary}, err
		}
	}
	for _, id := range ids {
		if k := lookupEnvKey(id); k != "" {
			key, err := validateEnvKey(k)
			return resolvedEnvKey{key: key, source: keyFromStore, appID: id}, err
		}
	}

	if !allowPrompt || !term.IsTerminal(os.Stdin.Fd()) {
		return resolvedEnvKey{}, fmt.Errorf("no encryption key for %q — pass --env-key, set %s, or run 'neo env key set %s'", primary, envKeyVar, primary)
	}

	var entered string
	if err := huh.NewInput().
		Title("Encryption key for " + primary).
		Description("From `php artisan env:encrypt` (starts with base64:)").
		EchoMode(huh.EchoModePassword).
		Value(&entered).
		Run(); err != nil {
		return resolvedEnvKey{}, err
	}
	key, err := validateEnvKey(entered)
	if err != nil {
		return resolvedEnvKey{}, err
	}
	return resolvedEnvKey{key: key, source: keyFromPrompt, appID: primary}, nil
}

// offerToSaveEnvKey asks whether to cache a freshly prompted key. Called only
// after the key has decrypted the file, so the store never holds a bad key.
func offerToSaveEnvKey(rk resolvedEnvKey) {
	if rk.source != keyFromPrompt || !term.IsTerminal(os.Stdin.Fd()) {
		return
	}
	save := true
	if err := huh.NewConfirm().
		Title("Save this key for " + rk.appID + "?").
		Description("Stored in " + envKeyStorePath() + " (0600, plain text)").
		Value(&save).
		Run(); err != nil || !save {
		return
	}
	if err := rememberEnvKey(rk.appID, rk.key); err != nil {
		ui.Error(fmt.Sprintf("could not save key: %s", err))
	}
}

// decryptHint points at the fix for a failed decrypt. A stale key in the store
// would otherwise fail every deploy with no clue where the key came from.
func decryptHint(rk resolvedEnvKey) {
	switch rk.source {
	case keyFromStore:
		ui.Info(fmt.Sprintf("This key came from %s. Clear it with 'neo env key forget %s', then retry.", envKeyStorePath(), rk.appID))
	case keyFromEnvVar:
		ui.Info(fmt.Sprintf("This key came from the %s / %s environment variable.", envKeyVar, laravelEnvKeyVar))
	}
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// validateEnvKey checks a key parses before it is used or stored, so a typo
// fails with a clear message instead of a MAC error later.
func validateEnvKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if _, err := laravel.ParseKey(key); err != nil {
		return "", fmt.Errorf("invalid encryption key: %w", err)
	}
	return key, nil
}

// loadEncryptedEnvFile decrypts a Laravel .env.encrypted file and parses the
// result as a .env. The plaintext is never written to disk.
func loadEncryptedEnvFile(path, key string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	parsedKey, err := laravel.ParseKey(key)
	if err != nil {
		return nil, err
	}
	plaintext, err := laravel.Decrypt(string(raw), parsedKey)
	if err != nil {
		return nil, err
	}
	env, err := parseEnvContent(plaintext)
	if err != nil {
		return nil, fmt.Errorf("parse decrypted %s: %w", filepath.Base(path), err)
	}
	return env, nil
}

// resolveEncryptedEnvPath decides which encrypted env file a deploy should read.
// Returns the absolute path plus whether it came from an explicit
// env_encrypted: in .neo.yml (explicit files must exist; auto-detected ones
// are best effort).
//
// Auto-detection only kicks in when .env.encrypted is unambiguously the env
// source: no env_file: configured and no plaintext .env sitting next to it.
func resolveEncryptedEnvPath(projectDir string, neoConfig *NeoConfig) (path string, explicit bool) {
	if neoConfig != nil && neoConfig.EnvEncrypted != "" {
		p := neoConfig.EnvEncrypted
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectDir, p)
		}
		return p, true
	}
	if neoConfig != nil && neoConfig.EnvFile != "" {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(projectDir, ".env")); err == nil {
		return "", false
	}
	auto := filepath.Join(projectDir, ".env.encrypted")
	if _, err := os.Stat(auto); err == nil {
		return auto, false
	}
	return "", false
}

// loadDeployEncryptedEnv resolves the key and decrypts the encrypted env file
// for a deploy, returning nil when the project has none. Errors are fatal:
// silently deploying without the secrets an app expects is worse than stopping.
func loadDeployEncryptedEnv(projectDir, appID string, neoConfig *NeoConfig, flagKey string, allowPrompt bool) (map[string]string, error) {
	path, explicit := resolveEncryptedEnvPath(projectDir, neoConfig)
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if explicit {
			return nil, fmt.Errorf("env_encrypted: %s not found", path)
		}
		return nil, nil
	}

	// Fall back to the bare project name so a key saved at the project root is
	// still found when deploying an environment-suffixed app (my-app-staging).
	rk, err := resolveEnvKey([]string{appID, defaultEnvKeyApp(projectDir)}, flagKey, allowPrompt)
	if err != nil {
		return nil, err
	}
	env, err := loadEncryptedEnvFile(path, rk.key)
	if err != nil {
		decryptHint(rk)
		return nil, fmt.Errorf("decrypt %s: %w", filepath.Base(path), err)
	}
	offerToSaveEnvKey(rk)
	return env, nil
}

// ---------------------------------------------------------------------------
// neo env encrypt / decrypt / key
// ---------------------------------------------------------------------------

func newEnvEncryptCmd() *cobra.Command {
	var (
		keyFlag   string
		appFlag   string
		forceFlag bool
		pruneFlag bool
	)

	cmd := &cobra.Command{
		Use:   "encrypt [file]",
		Short: "Encrypt a .env file (Laravel env:encrypt format)",
		Long:  "Encrypts a .env file into <file>.encrypted, readable by both neo and `php artisan env:decrypt`.\nWith no key, a new one is generated and printed once — store it in a password manager.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := ".env"
			if len(args) > 0 {
				source = args[0]
			}
			return runEnvEncrypt(source, keyFlag, appFlag, forceFlag, pruneFlag)
		},
	}

	cmd.Flags().StringVar(&keyFlag, "key", "", "encryption key (default: generate a new one)")
	cmd.Flags().StringVar(&appFlag, "app", "", "save the key under this app name (default: directory name)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "overwrite an existing encrypted file")
	cmd.Flags().BoolVar(&pruneFlag, "prune", false, "delete the plaintext file after encrypting")

	return cmd
}

func newEnvDecryptCmd() *cobra.Command {
	var (
		keyFlag    string
		appFlag    string
		forceFlag  bool
		stdoutFlag bool
	)

	cmd := &cobra.Command{
		Use:   "decrypt [file]",
		Short: "Decrypt a .env.encrypted file",
		Long:  "Decrypts a Laravel-encrypted env file. Writes the plaintext next to it (suffix stripped) or prints it with --stdout.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := ".env.encrypted"
			if len(args) > 0 {
				source = args[0]
			}
			return runEnvDecrypt(source, keyFlag, appFlag, forceFlag, stdoutFlag)
		},
	}

	cmd.Flags().StringVar(&keyFlag, "key", "", "decryption key (default: NEO_ENV_KEY, saved key, or prompt)")
	cmd.Flags().StringVar(&appFlag, "app", "", "look up the saved key under this app name (default: directory name)")
	cmd.Flags().BoolVar(&forceFlag, "force", false, "overwrite an existing plaintext file")
	cmd.Flags().BoolVar(&stdoutFlag, "stdout", false, "print to stdout instead of writing a file")

	return cmd
}

func newEnvKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Manage saved env encryption keys",
		Long:  "Keys are stored in ~/.neo/keys.json (0600) so deploys don't prompt every time.",
	}

	cmd.AddCommand(
		&cobra.Command{
			Use:   "list",
			Short: "List apps with a saved encryption key",
			Args:  cobra.NoArgs,
			RunE:  func(cmd *cobra.Command, args []string) error { return runEnvKeyList() },
		},
		&cobra.Command{
			Use:   "set <app> [key]",
			Short: "Save an encryption key for an app",
			Args:  cobra.RangeArgs(1, 2),
			RunE: func(cmd *cobra.Command, args []string) error {
				key := ""
				if len(args) > 1 {
					key = args[1]
				}
				return runEnvKeySet(args[0], key)
			},
		},
		&cobra.Command{
			Use:   "forget <app>",
			Short: "Remove a saved encryption key",
			Args:  cobra.ExactArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return runEnvKeyForget(args[0]) },
		},
	)

	return cmd
}

func runEnvEncrypt(source, keyFlag, appFlag string, force, prune bool) error {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	plaintext, err := os.ReadFile(absSource)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}

	target := absSource + ".encrypted"
	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("%s already exists — use --force to overwrite", filepath.Base(target))
	}

	appID := appFlag
	if appID == "" {
		appID = defaultEnvKeyApp(filepath.Dir(absSource))
	}

	key := keyFlag
	generated := false
	if key == "" {
		if saved := lookupEnvKey(appID); saved != "" {
			key = saved
		} else {
			key, err = laravel.GenerateKey(32)
			if err != nil {
				return err
			}
			generated = true
		}
	}
	key, err = validateEnvKey(key)
	if err != nil {
		return err
	}
	parsedKey, err := laravel.ParseKey(key)
	if err != nil {
		return err
	}

	payload, err := laravel.Encrypt(string(plaintext), parsedKey)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}
	if err := os.WriteFile(target, []byte(payload), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}

	if prune {
		if err := os.Remove(absSource); err != nil {
			ui.Error(fmt.Sprintf("could not delete %s: %s", source, err))
		}
	}

	fmt.Println()
	ui.Success(fmt.Sprintf("Encrypted → %s", filepath.Base(target)))
	card := ui.NewCard()
	card.AddKV("Key", key)
	card.AddKV("Cipher", laravel.CipherName(parsedKey))
	card.AddKV("File", filepath.Base(target))
	card.Render()

	if generated {
		ui.Error("This key is shown once. Store it in a password manager now — without it the file cannot be decrypted.")
		if err := rememberEnvKey(appID, key); err == nil {
			ui.Info(fmt.Sprintf("Saved locally for %q (%s)", appID, envKeyStorePath()))
		}
	}
	ui.Info("Commit the .encrypted file. Never commit the key or the plaintext .env.")
	ui.Info(fmt.Sprintf("Add to .neo.yml:  env_encrypted: %s", filepath.Base(target)))
	fmt.Println()

	return nil
}

func runEnvDecrypt(source, keyFlag, appFlag string, force, toStdout bool) error {
	absSource, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if _, err := os.Stat(absSource); err != nil {
		return fmt.Errorf("%s not found", source)
	}

	appID := appFlag
	if appID == "" {
		appID = defaultEnvKeyApp(filepath.Dir(absSource))
	}
	rk, err := resolveEnvKey([]string{appID}, keyFlag, true)
	if err != nil {
		return err
	}

	raw, err := os.ReadFile(absSource)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	parsedKey, err := laravel.ParseKey(rk.key)
	if err != nil {
		return err
	}
	plaintext, err := laravel.Decrypt(string(raw), parsedKey)
	if err != nil {
		decryptHint(rk)
		return fmt.Errorf("decrypt %s: %w", filepath.Base(absSource), err)
	}
	offerToSaveEnvKey(rk)

	if toStdout {
		fmt.Print(plaintext)
		if !strings.HasSuffix(plaintext, "\n") {
			fmt.Println()
		}
		return nil
	}

	target := strings.TrimSuffix(absSource, ".encrypted")
	if target == absSource {
		target = absSource + ".decrypted"
	}
	if _, err := os.Stat(target); err == nil && !force {
		return fmt.Errorf("%s already exists — use --force to overwrite", filepath.Base(target))
	}
	if err := os.WriteFile(target, []byte(plaintext), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}

	fmt.Println()
	decrypted, parseErr := parseEnvContent(plaintext)
	if parseErr != nil {
		ui.Error(fmt.Sprintf("decrypted, but the contents did not parse cleanly: %s", parseErr))
	}
	ui.Success(fmt.Sprintf("Decrypted → %s (%d vars)", filepath.Base(target), len(decrypted)))
	ui.Info("Plaintext written with 0600 permissions. Keep it out of git.")
	fmt.Println()

	return nil
}

func runEnvKeyList() error {
	store, err := loadEnvKeyStore()
	if err != nil {
		return err
	}
	if len(store.Keys) == 0 {
		fmt.Println()
		ui.Info("No saved encryption keys.")
		ui.Info("Save one with: neo env key set <app> <key>")
		fmt.Println()
		return nil
	}

	names := make([]string, 0, len(store.Keys))
	for name := range store.Keys {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Println()
	fmt.Printf("  %s\n\n", ui.Bold.Render("Saved env encryption keys"))
	for _, name := range names {
		fmt.Printf("  %-24s %s\n", name, ui.Faint.Render(maskEnvKey(store.Keys[name])))
	}
	fmt.Printf("\n  %s\n\n", ui.Faint.Render(envKeyStorePath()))

	return nil
}

func runEnvKeySet(appID, key string) error {
	if key == "" {
		if !term.IsTerminal(os.Stdin.Fd()) {
			return fmt.Errorf("provide the key as an argument when running non-interactively")
		}
		if err := huh.NewInput().
			Title("Encryption key for " + appID).
			EchoMode(huh.EchoModePassword).
			Value(&key).
			Run(); err != nil {
			return err
		}
	}

	validated, err := validateEnvKey(key)
	if err != nil {
		return err
	}
	if err := rememberEnvKey(appID, validated); err != nil {
		return err
	}

	fmt.Println()
	ui.Success(fmt.Sprintf("Saved encryption key for %q", appID))
	ui.Info(envKeyStorePath() + " (0600, plain text)")
	fmt.Println()

	return nil
}

func runEnvKeyForget(appID string) error {
	removed, err := forgetEnvKey(appID)
	if err != nil {
		return err
	}
	fmt.Println()
	if removed {
		ui.Success(fmt.Sprintf("Removed saved key for %q", appID))
	} else {
		ui.Info(fmt.Sprintf("No saved key for %q", appID))
	}
	fmt.Println()
	return nil
}

// defaultEnvKeyApp derives the key-store identity from a project directory,
// matching how deploy names an app when .neo.yml has no name:.
func defaultEnvKeyApp(projectDir string) string {
	if cfg, err := loadNeoConfig(projectDir); err == nil && cfg != nil && cfg.Name != "" {
		return sanitizeName(cfg.Name)
	}
	return sanitizeName(filepath.Base(projectDir))
}

// maskEnvKey shows enough of a key to recognise it, never enough to use it.
func maskEnvKey(key string) string {
	body := strings.TrimPrefix(key, "base64:")
	if len(body) <= 8 {
		return "••••••••"
	}
	return body[:4] + "••••••••" + body[len(body)-4:]
}
