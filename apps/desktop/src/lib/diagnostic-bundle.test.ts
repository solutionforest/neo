import { describe, expect, it } from "vitest";
import {
  buildDiagnosticBundle,
  bundleFilename,
  maskHost,
  redactText,
  REDACTED,
  serializeBundle,
  type BundleInput,
} from "./diagnostic-bundle";
import type { ObservabilityEvent, VersionInfo } from "./observability";
import type { ServerSummary } from "./protocol";

const VERSIONS: VersionInfo = {
  desktopVersion: "0.1.0",
  bridgeVersion: "0.1.0",
  coreVersion: "0.2.0",
  protocolVersion: 1,
  commit: "abc123",
  platform: "darwin",
  arch: "arm64",
  activation: "active",
};

const SERVERS: ServerSummary[] = [
  { id: "prod", name: "production", host: "root@203.0.113.7", current: true },
  { id: "stg", name: "staging", host: "10.0.0.5", current: false },
];

function baseInput(overrides: Partial<BundleInput> = {}): BundleInput {
  return {
    versions: VERSIONS,
    servers: SERVERS,
    events: [],
    generatedAt: "2026-07-18T08:00:00.000Z",
    ...overrides,
  };
}

describe("redactText", () => {
  it("removes PEM private key blocks", () => {
    const text =
      "key -----BEGIN OPENSSH PRIVATE KEY-----\nAAAAsecretmaterial\n-----END OPENSSH PRIVATE KEY----- done";
    const out = redactText(text);
    expect(out).not.toContain("secretmaterial");
    expect(out).toContain(REDACTED);
  });

  it("masks secret-looking assignments while keeping the key name", () => {
    expect(redactText("DB_PASSWORD=hunter2")).toBe(`DB_PASSWORD=${REDACTED}`);

    const token = redactText('"token": "abcd-efgh"');
    expect(token).toContain(REDACTED);
    expect(token).not.toContain("abcd-efgh");

    const license = redactText("--license NEO-XXXX-YYYY");
    expect(license).toContain(REDACTED);
    expect(license).not.toContain("NEO-XXXX-YYYY");
  });

  it("redacts long opaque blobs that could be key material", () => {
    const blob = "A".repeat(50);
    expect(redactText(`value ${blob}`)).toBe(`value ${REDACTED}`);
  });

  it("leaves ordinary text untouched", () => {
    expect(redactText("poll scheduled in 30000ms")).toBe("poll scheduled in 30000ms");
  });
});

describe("maskHost", () => {
  it("keeps the login user but masks the address", () => {
    expect(maskHost("root@203.0.113.7")).toBe(`root@${REDACTED}`);
  });
  it("masks a bare host entirely", () => {
    expect(maskHost("10.0.0.5")).toBe(REDACTED);
  });
});

describe("buildDiagnosticBundle", () => {
  it("excludes server logs by default and lists what is omitted", () => {
    const bundle = buildDiagnosticBundle(
      baseInput({ serverLogs: ["line one", "line two"] }),
    );
    expect(bundle.serverLogs).toBeUndefined();
    expect(bundle.excludes).toContain("Private keys and their contents");
    expect(bundle.excludes).toContain("Full server logs (not included)");
    expect(bundle.redacted).toBe(true);
  });

  it("includes redacted server logs only after explicit opt-in", () => {
    const bundle = buildDiagnosticBundle(
      baseInput({
        serverLogs: ["password=secret123 in log", "normal line"],
        options: { includeServerLogs: true },
      }),
    );
    expect(bundle.serverLogs).toBeDefined();
    expect(bundle.serverLogs?.[0]).toContain(REDACTED);
    expect(bundle.serverLogs?.[0]).not.toContain("secret123");
    expect(bundle.excludes).not.toContain("Full server logs (not included)");
  });

  it("masks server hosts in config metadata", () => {
    const bundle = buildDiagnosticBundle(baseInput());
    expect(bundle.config.serverCount).toBe(2);
    expect(bundle.config.servers[0].host).toBe(`root@${REDACTED}`);
    expect(bundle.config.servers[1].host).toBe(REDACTED);
  });

  it("scrubs secret-bearing observability fields", () => {
    const events: ObservabilityEvent[] = [
      {
        at: 1,
        category: "request",
        message: "server_snapshot ok",
        fields: { method: "server_snapshot", durationMs: 40, code: null },
      },
      {
        at: 2,
        category: "bridge",
        // A hypothetical rogue field name should still be scrubbed by key.
        message: "detail with password=hunter2",
        fields: { token: "should-not-survive", durationMs: 5 },
      },
    ];
    const bundle = buildDiagnosticBundle(baseInput({ events }));
    const serialized = serializeBundle(bundle);
    expect(serialized).not.toContain("hunter2");
    expect(serialized).not.toContain("should-not-survive");
    expect(bundle.events[1].fields.token).toBe(REDACTED);
    // The non-secret request event survives intact.
    expect(bundle.events[0].fields.method).toBe("server_snapshot");
  });

  it("produces a stable, secret-free filename", () => {
    expect(bundleFilename("2026-07-18T08:00:00.000Z")).toBe(
      "neo-desktop-diagnostics-2026-07-18T08-00-00-000Z.json",
    );
  });

  it("never leaks a license key from a field value or an assignment", () => {
    const events: ObservabilityEvent[] = [
      {
        at: 1,
        category: "update",
        message: "activation license=NEO-ABCD-EFGH-IJKL",
        fields: { licenseKey: "NEO-ABCD-EFGH-IJKL" },
      },
    ];
    const serialized = serializeBundle(buildDiagnosticBundle(baseInput({ events })));
    expect(serialized).not.toContain("NEO-ABCD-EFGH-IJKL");
  });
});
