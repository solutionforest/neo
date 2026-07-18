export type IconName =
  | "activity"
  | "apps"
  | "check"
  | "chevron"
  | "clock"
  | "close"
  | "cpu"
  | "disk"
  | "info"
  | "latency"
  | "logs"
  | "memory"
  | "refresh"
  | "search"
  | "server"
  | "warning";

export interface IconProps {
  name: IconName;
  size?: number;
  className?: string;
}

/**
 * Small, stroke-based interface symbols. They inherit the surrounding color,
 * stay crisp at desktop control sizes, and avoid adding an icon dependency or
 * widening the webview's CSP surface.
 */
export function Icon({ name, size = 16, className }: IconProps) {
  const common = {
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    fill: "none",
    stroke: "currentColor",
    strokeWidth: 1.8,
    strokeLinecap: "round" as const,
    strokeLinejoin: "round" as const,
    "aria-hidden": true,
    className: ["icon", className].filter(Boolean).join(" "),
  };

  switch (name) {
    case "activity":
      return <svg {...common}><path d="M3 12h4l2.2-6 4.1 12 2.2-6H21" /></svg>;
    case "apps":
      return <svg {...common}><rect x="3" y="3" width="7" height="7" rx="2" /><rect x="14" y="3" width="7" height="7" rx="2" /><rect x="3" y="14" width="7" height="7" rx="2" /><rect x="14" y="14" width="7" height="7" rx="2" /></svg>;
    case "check":
      return <svg {...common}><path d="m5 12 4 4L19 6" /></svg>;
    case "chevron":
      return <svg {...common}><path d="m9 6 6 6-6 6" /></svg>;
    case "clock":
      return <svg {...common}><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></svg>;
    case "close":
      return <svg {...common}><path d="m6 6 12 12M18 6 6 18" /></svg>;
    case "cpu":
      return <svg {...common}><rect x="7" y="7" width="10" height="10" rx="2" /><path d="M9 2v3m6-3v3M9 19v3m6-3v3M2 9h3m-3 6h3m14-6h3m-3 6h3" /></svg>;
    case "disk":
      return <svg {...common}><rect x="4" y="3" width="16" height="18" rx="3" /><circle cx="12" cy="10" r="3" /><path d="M8 17h8" /></svg>;
    case "info":
      return <svg {...common}><circle cx="12" cy="12" r="9" /><path d="M12 11v5m0-8h.01" /></svg>;
    case "latency":
      return <svg {...common}><path d="M4.9 16.5a10 10 0 0 1 14.2 0M8.5 13a5 5 0 0 1 7 0M12 18h.01" /></svg>;
    case "logs":
      return <svg {...common}><path d="M5 4h14v16H5zM8 8h8M8 12h8M8 16h5" /></svg>;
    case "memory":
      return <svg {...common}><rect x="3" y="6" width="18" height="12" rx="2" /><path d="M7 10h2v4H7m5-4h2v4h-2m5-4h.01M7 3v3m5-3v3m5-3v3M7 18v3m5-3v3m5-3v3" /></svg>;
    case "refresh":
      return <svg {...common}><path d="M20 6v5h-5M4 18v-5h5" /><path d="M18.3 9A7 7 0 0 0 6.1 6.7L4 11m16 2-2.1 4.3A7 7 0 0 1 5.7 15" /></svg>;
    case "search":
      return <svg {...common}><circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" /></svg>;
    case "server":
      return <svg {...common}><rect x="3" y="4" width="18" height="6" rx="2" /><rect x="3" y="14" width="18" height="6" rx="2" /><path d="M7 7h.01M7 17h.01M11 7h6M11 17h6" /></svg>;
    case "warning":
      return <svg {...common}><path d="M10.3 4.2 2.8 17a2 2 0 0 0 1.7 3h15a2 2 0 0 0 1.7-3L13.7 4.2a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4m0 3h.01" /></svg>;
  }
}
