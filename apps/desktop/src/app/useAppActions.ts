import { useEffect, useMemo, useRef, useState } from "react";
import type { DesktopAPI } from "../lib/desktop-api";
import {
  ActionController,
  type ActionControllerState,
} from "../lib/action-controller";
import { ActionHistory, type HistoryStorage } from "../lib/actions";
import type { AppAction } from "../lib/protocol";

export interface UseAppActionsOptions {
  api: DesktopAPI;
  server: string;
  /** Called after every settled action so the caller can refresh immediately. */
  onSettled?: () => void;
  /** Injectable persistence for the action history (defaults to localStorage). */
  storage?: HistoryStorage;
}

export interface AppActions {
  state: ActionControllerState;
  request: (app: string, action: AppAction) => void;
  confirm: () => void;
  cancel: () => void;
  dismiss: () => void;
  setRemember: (value: boolean) => void;
  isRunning: (app: string) => boolean;
}

/**
 * Subscribes a component to a single ActionController (the owner of the
 * confirmation flow, duplicate-click protection, cancellation, and history).
 * The controller outlives re-renders and server switches; only `onSettled` and
 * the selected server are updated in place.
 */
export function useAppActions({
  api,
  server,
  onSettled,
  storage,
}: UseAppActionsOptions): AppActions {
  const onSettledRef = useRef(onSettled);
  onSettledRef.current = onSettled;

  const controller = useMemo(
    () =>
      new ActionController({
        api,
        server,
        history: new ActionHistory(storage ?? defaultStorage()),
        onSettled: () => onSettledRef.current?.(),
      }),
    // The controller is created once per api/storage; the server is pushed in
    // via setServer below so switching servers does not drop in-flight state.
    [api, storage],
  );

  const [state, setState] = useState<ActionControllerState>(() => controller.getState());

  useEffect(() => {
    controller.setServer(server);
  }, [controller, server]);

  useEffect(() => controller.subscribe(setState), [controller]);

  return useMemo<AppActions>(
    () => ({
      state,
      request: (app, action) => controller.request(app, action),
      confirm: () => controller.confirm(),
      cancel: () => controller.cancel(),
      dismiss: () => controller.dismiss(),
      setRemember: (value) => controller.setRemember(value),
      isRunning: (app) => state.runningApps.includes(app),
    }),
    [controller, state],
  );
}

/** localStorage when present (production/jsdom), otherwise no persistence. */
function defaultStorage(): HistoryStorage | undefined {
  try {
    if (typeof localStorage !== "undefined") return localStorage;
  } catch {
    // Access can throw in locked-down contexts; fall back to in-memory.
  }
  return undefined;
}
