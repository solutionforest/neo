/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Opt into the real Tauri bridge transport (slice 2+). */
  readonly VITE_USE_BRIDGE?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
