import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { AppActionDialog } from "./AppActionDialog";
import type { ActionDialogState } from "../../lib/action-controller";
import { BridgeError } from "../../lib/protocol";

function dialog(overrides: Partial<ActionDialogState> = {}): ActionDialogState {
  return {
    app: "ghost",
    action: "stop",
    safety: "availability",
    impact: "“ghost” will be stopped and unavailable until it is started again.",
    phase: "confirm",
    canRemember: false,
    remember: false,
    operationId: null,
    result: null,
    error: null,
    ...overrides,
  };
}

const noop = () => {};

describe("AppActionDialog", () => {
  it("shows target server, app, action and availability impact", () => {
    render(
      <AppActionDialog
        server="production"
        dialog={dialog()}
        onConfirm={noop}
        onCancel={noop}
        onDismiss={noop}
        onRememberChange={noop}
        onViewLogs={noop}
      />,
    );
    expect(screen.getByRole("dialog", { name: "Stop ghost" })).toBeInTheDocument();
    expect(screen.getByText("production")).toBeInTheDocument();
    expect(screen.getByText(/unavailable until it is started again/)).toBeInTheDocument();
    // stop is availability-affecting: no "don't ask again" option.
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  });

  it("offers a remember option for reversible actions", () => {
    const onRemember = vi.fn();
    render(
      <AppActionDialog
        server="production"
        dialog={dialog({ action: "start", safety: "reversible", canRemember: true })}
        onConfirm={noop}
        onCancel={noop}
        onDismiss={noop}
        onRememberChange={onRemember}
        onViewLogs={noop}
      />,
    );
    const box = screen.getByRole("checkbox");
    fireEvent.click(box);
    expect(onRemember).toHaveBeenCalledWith(true);
  });

  it("shows a Cancel button while running", () => {
    const onCancel = vi.fn();
    render(
      <AppActionDialog
        server="production"
        dialog={dialog({ phase: "running", operationId: "op-1" })}
        onConfirm={noop}
        onCancel={onCancel}
        onDismiss={noop}
        onRememberChange={noop}
        onViewLogs={noop}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onCancel).toHaveBeenCalled();
  });

  it("offers a log link on failure", () => {
    const onViewLogs = vi.fn();
    render(
      <AppActionDialog
        server="production"
        dialog={dialog({
          phase: "done",
          error: new BridgeError("internal_error", "docker refused"),
        })}
        onConfirm={noop}
        onCancel={noop}
        onDismiss={noop}
        onRememberChange={noop}
        onViewLogs={onViewLogs}
      />,
    );
    expect(screen.getByText("docker refused")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "View logs" }));
    expect(onViewLogs).toHaveBeenCalledWith("ghost");
  });

  it("shows changes on a successful result", () => {
    render(
      <AppActionDialog
        server="production"
        dialog={dialog({
          action: "start",
          phase: "done",
          result: {
            operationId: "op-1",
            status: "succeeded",
            startedAt: "s",
            finishedAt: "f",
            summary: "started ghost",
            changes: [{ target: "ghost", from: "stopped", to: "running" }],
          },
        })}
        onConfirm={noop}
        onCancel={noop}
        onDismiss={noop}
        onRememberChange={noop}
        onViewLogs={noop}
      />,
    );
    expect(screen.getByText("started ghost")).toBeInTheDocument();
    expect(screen.getByText(/stopped → running/)).toBeInTheDocument();
  });
});
