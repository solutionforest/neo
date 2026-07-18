//! `neo-bridge` sidecar supervision — placeholder for slice 2.
//!
//! Slice 2 will spawn the bundled `neo-bridge` binary as a Tauri-owned child
//! process, exchange versioned newline-delimited JSON over stdio, correlate
//! responses by request id, and restart it with backoff on unexpected exit
//! (see plan "Phase 2: introduce the bridge walking skeleton"). The module
//! exists now so the trust-boundary layering from the target layout is present
//! from the first slice.

/// Protocol version this desktop build speaks. Mirrors `PROTOCOL_VERSION` on the
/// TypeScript side and the Go bridge.
pub const PROTOCOL_VERSION: u32 = 1;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn protocol_version_starts_at_one() {
        // Bump deliberately alongside the TypeScript and Go definitions.
        assert_eq!(PROTOCOL_VERSION, 1);
    }
}
