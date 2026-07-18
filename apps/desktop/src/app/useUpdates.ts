import { useEffect, useMemo, useSyncExternalStore } from "react";
import {
  UpdateController,
  createTauriUpdateBackend,
  type UpdateControllerState,
} from "../lib/update-controller";

export interface UpdatesHook {
  state: UpdateControllerState;
  install: () => void;
  defer: () => void;
  dismissError: () => void;
}

/**
 * Own the updater life cycle for the always-alive popover window: silent check
 * shortly after startup and every six hours (see UpdateController). Only the
 * popover uses this hook, so opening the management window never multiplies
 * update checks — the same single-owner rule as the polling service.
 *
 * Tests inject a controller; production builds default to the Tauri backend.
 */
export function useUpdates(controller?: UpdateController): UpdatesHook {
  const owned = useMemo(
    () => controller ?? new UpdateController(createTauriUpdateBackend()),
    [controller],
  );

  useEffect(() => {
    owned.start();
    return () => owned.stop();
  }, [owned]);

  const state = useSyncExternalStore(
    (onChange) => owned.subscribe(onChange),
    () => owned.getState(),
  );

  return {
    state,
    install: () => void owned.install(),
    defer: () => owned.defer(),
    dismissError: () => owned.dismissError(),
  };
}
