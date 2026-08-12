import { useEffect, useState } from "react";
import { Popover } from "./features/Popover";
import { Dashboard } from "./features/Dashboard";
import { ErrorBoundary } from "./components/ErrorBoundary";

// Render the popover or the full dashboard depending on which Tauri window we're in.
function useWindowLabel(): string {
  const [label, setLabel] = useState<string>("popover");
  useEffect(() => {
    (async () => {
      try {
        const { getCurrentWindow } = await import("@tauri-apps/api/window");
        setLabel(getCurrentWindow().label);
      } catch {
        setLabel("popover");
      }
    })();
  }, []);
  return label;
}

export default function App() {
  const label = useWindowLabel();
  return <ErrorBoundary>{label === "main" ? <Dashboard /> : <Popover />}</ErrorBoundary>;
}
