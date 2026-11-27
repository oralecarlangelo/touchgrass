import { Component, type ReactNode } from 'react';

import ErrorState from './ErrorState.tsx';

export default class ScreenErrorBoundary extends Component<{ children: ReactNode; title?: string }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (this.state.failed) {
      return (
        <ErrorState
          title={this.props.title ?? 'Couldn’t load this screen'}
          message="Reload to get the latest version of Touch Grass."
          onRetry={() => window.location.reload()}
        />
      );
    }
    return this.props.children;
  }
}
