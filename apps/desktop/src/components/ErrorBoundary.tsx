import { Component, type ErrorInfo, type ReactNode } from "react";

interface State {
  message: string | null;
}

// Keeps a render crash from blanking the whole popover — shows the error instead.
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { message: null };

  static getDerivedStateFromError(error: unknown): State {
    return { message: error instanceof Error ? error.message : String(error) };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("UI error:", error, info);
  }

  render() {
    if (this.state.message) {
      return (
        <div className="popover">
          <div className="offline">
            UI error: {this.state.message}
            <button className="btn" style={{ marginTop: 10 }} onClick={() => this.setState({ message: null })}>
              Dismiss
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}
