import type { DesktopAPI } from "./protocol";
import { fixtureApi } from "./desktop-api";
import { tauriApi } from "./tauri-api";

// Use the real bridge inside Tauri; fall back to fixtures in a plain browser
// (Storybook-style development and tests).
const isTauri = typeof window !== "undefined" && "__TAURI_INTERNALS__" in window;

export const api: DesktopAPI = isTauri ? tauriApi : fixtureApi;
export const usingFixtures = !isTauri;
