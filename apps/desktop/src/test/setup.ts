import "@testing-library/jest-dom/vitest";
import { afterEach } from "vitest";
import { cleanup } from "@testing-library/react";

// Ensure the DOM is reset between tests so multiple render() calls don't leak.
afterEach(() => {
  cleanup();
});
