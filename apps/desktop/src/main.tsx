import React from "react";
import ReactDOM from "react-dom/client";
import { App, type WindowKind } from "./app/App";
import "./styles/global.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("missing #root element");
}

// Each HTML entry marks which window it is via data-window; the tray popover
// and the management window share one bundle but render different roots.
const kind: WindowKind =
  root.dataset.window === "management" ? "management" : "popover";

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <App window={kind} />
  </React.StrictMode>,
);
