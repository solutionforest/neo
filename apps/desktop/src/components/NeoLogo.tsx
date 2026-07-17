// Inline SVG brand mark so the popover header renders without loading an asset
// (keeps the strict CSP img-src surface small). Mirrors the generated app icon.
export function NeoLogo({ size = 22 }: { size?: number }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 100 100"
      role="img"
      aria-label="Neo"
      className="neo-logo"
    >
      <rect x="2" y="2" width="96" height="96" rx="22" fill="#4f46e5" />
      <path
        d="M56 8 L30 55 L46 55 L40 92 L70 42 L52 42 L62 8 Z"
        fill="#f5f7ff"
      />
    </svg>
  );
}
