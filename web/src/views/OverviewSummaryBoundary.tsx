import { Component, type ReactNode } from "react";

/** A failed lazy summary gets one bounded retry without affecting the rest of Overview. */
export class OverviewSummaryBoundary extends Component<{ children: ReactNode; fallback?: ReactNode }, { failed: boolean }> {
  state = { failed: false };
  private retry?: ReturnType<typeof setTimeout>;

  static getDerivedStateFromError() {
    return { failed: true };
  }

  componentDidCatch() {
    this.retry = setTimeout(() => this.setState({ failed: false }), 10_000);
  }

  componentWillUnmount() {
    clearTimeout(this.retry);
  }

  render() {
    return this.state.failed ? (this.props.fallback ?? null) : this.props.children;
  }
}
