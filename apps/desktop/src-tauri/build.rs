use std::path::PathBuf;
use std::process::Command;

fn main() {
    build_bridge_sidecar();
    tauri_build::build();
}

/// Build the Go `neo-bridge` sidecar into `binaries/neo-bridge-<target-triple>`
/// so Tauri's `externalBin` reference resolves during `cargo build`/`tauri
/// build`. Keeping this in the Rust build (rather than a separate CI step) makes
/// the desktop build self-contained on any machine that has the Go toolchain —
/// which every developer of this Go+Rust project already needs.
///
/// This runs the host Go toolchain directly; it never touches the Dockerized CLI
/// build path (`make build`) and never adds anything to the root Go module.
fn build_bridge_sidecar() {
    // Rebuild only when the bridge sources change, so `cargo` stays cache-friendly.
    let repo_root = repo_root();
    let bridge_dir = repo_root.join("cmd").join("neo-bridge");
    println!("cargo:rerun-if-changed={}", bridge_dir.display());
    println!(
        "cargo:rerun-if-changed={}",
        repo_root.join("go.mod").display()
    );
    if let Ok(entries) = std::fs::read_dir(&bridge_dir) {
        for entry in entries.flatten() {
            println!("cargo:rerun-if-changed={}", entry.path().display());
        }
    }

    let target = std::env::var("TARGET").unwrap_or_default();
    let (goos, goarch) = go_target(&target);

    let out_dir = manifest_dir().join("binaries");
    std::fs::create_dir_all(&out_dir).expect("create binaries dir");
    let mut out_name = format!("neo-bridge-{target}");
    if goos == "windows" {
        out_name.push_str(".exe");
    }
    let out_path = out_dir.join(&out_name);

    // Stamp bridge/core versions and the build commit. Defaults come from the
    // desktop package version and `git rev-parse`; the release workflow
    // overrides both via env so tag builds are stamped exactly.
    println!("cargo:rerun-if-env-changed=NEO_DESKTOP_VERSION");
    println!("cargo:rerun-if-env-changed=NEO_DESKTOP_COMMIT");
    let version = std::env::var("NEO_DESKTOP_VERSION")
        .or_else(|_| std::env::var("CARGO_PKG_VERSION"))
        .unwrap_or_else(|_| "dev".into());
    let commit = std::env::var("NEO_DESKTOP_COMMIT")
        .ok()
        .filter(|c| !c.is_empty())
        .unwrap_or_else(|| git_commit(&repo_root));
    let ldflags = format!(
        "-s -w -X main.bridgeVersion={version} -X main.coreVersion={version} -X main.buildCommit={commit}"
    );

    let go = std::env::var("GO").unwrap_or_else(|_| "go".into());
    let mut cmd = Command::new(&go);
    cmd.current_dir(&repo_root)
        .env("CGO_ENABLED", "0")
        .env("GOOS", goos)
        .env("GOARCH", goarch)
        .arg("build")
        .arg("-trimpath")
        .arg("-ldflags")
        .arg(&ldflags)
        .arg("-o")
        .arg(&out_path)
        .arg("./cmd/neo-bridge");

    println!(
        "cargo:warning=building neo-bridge sidecar for {target} -> {}",
        out_path.display()
    );

    let status = cmd.status().unwrap_or_else(|e| {
        panic!(
            "failed to invoke `{go} build` for the neo-bridge sidecar: {e}. \
             Install the Go toolchain, or set GO to its path."
        )
    });
    if !status.success() {
        panic!("`go build ./cmd/neo-bridge` failed (status {status})");
    }
}

/// Short commit hash of the repo HEAD, or "unknown" outside a git checkout.
fn git_commit(repo_root: &std::path::Path) -> String {
    Command::new("git")
        .arg("-C")
        .arg(repo_root)
        .args(["rev-parse", "--short=12", "HEAD"])
        .output()
        .ok()
        .filter(|o| o.status.success())
        .and_then(|o| String::from_utf8(o.stdout).ok())
        .map(|s| s.trim().to_string())
        .filter(|s| !s.is_empty())
        .unwrap_or_else(|| "unknown".into())
}

fn manifest_dir() -> PathBuf {
    PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR"))
}

/// Repo root, three levels above `apps/desktop/src-tauri`.
fn repo_root() -> PathBuf {
    manifest_dir()
        .ancestors()
        .nth(3)
        .expect("repo root above apps/desktop/src-tauri")
        .to_path_buf()
}

/// Map a Rust target triple to Go's GOOS/GOARCH.
fn go_target(triple: &str) -> (&'static str, &'static str) {
    let goos = if triple.contains("windows") {
        "windows"
    } else if triple.contains("apple") || triple.contains("darwin") {
        "darwin"
    } else {
        "linux"
    };
    let goarch = if triple.starts_with("x86_64") {
        "amd64"
    } else if triple.starts_with("aarch64") || triple.starts_with("arm64") {
        "arm64"
    } else if triple.starts_with("i686") || triple.starts_with("i586") {
        "386"
    } else if triple.starts_with("armv7") || triple.starts_with("arm") {
        "arm"
    } else {
        // Fall back to amd64 for unknown triples rather than failing the build.
        "amd64"
    };
    (goos, goarch)
}
