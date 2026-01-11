import { Component, ErrorInfo, ReactNode } from "react";

interface ErrorBoundaryProps {
  children: ReactNode;
  fallback?: ReactNode;
}

interface ErrorBoundaryState {
  hasError: boolean;
  error: Error | null;
  errorInfo: ErrorInfo | null;
}

/**
 * Error Boundary component to catch and handle rendering errors gracefully.
 * Prevents the entire app from crashing when a component throws an error.
 */
export class ErrorBoundary extends Component<
  ErrorBoundaryProps,
  ErrorBoundaryState
> {
  constructor(props: ErrorBoundaryProps) {
    super(props);
    this.state = {
      hasError: false,
      error: null,
      errorInfo: null,
    };
  }

  static getDerivedStateFromError(error: Error): Partial<ErrorBoundaryState> {
    return { hasError: true, error };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo): void {
    this.setState({ errorInfo });
    // Log error to console in development
    console.error("ErrorBoundary caught an error:", error, errorInfo);
  }

  handleRetry = (): void => {
    this.setState({ hasError: false, error: null, errorInfo: null });
  };

  render(): ReactNode {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback;
      }

      return (
        <ErrorFallback error={this.state.error} onRetry={this.handleRetry} />
      );
    }

    return this.props.children;
  }
}

interface ErrorFallbackProps {
  error: Error | null;
  onRetry?: () => void;
}

/**
 * Default error fallback UI component
 */
export function ErrorFallback({ error, onRetry }: ErrorFallbackProps) {
  return (
    <div className="min-h-[400px] flex items-center justify-center p-8">
      <div className="text-center max-w-md">
        <div className="w-20 h-20 mx-auto mb-6 bg-danger-light rounded-2xl flex items-center justify-center">
          <span className="text-4xl">⚠️</span>
        </div>
        <h2 className="text-xl font-bold text-text-primary mb-2">
          Something went wrong
        </h2>
        <p className="text-text-secondary mb-4">
          An unexpected error occurred while rendering this page.
        </p>
        {error && (
          <div className="p-3 mb-4 rounded-lg bg-danger-light/20 border border-danger-light text-left">
            <p className="text-sm font-mono text-danger-dark break-all">
              {error.message}
            </p>
          </div>
        )}
        {onRetry && (
          <button onClick={onRetry} className="btn btn-primary">
            <span>🔄</span>
            Try Again
          </button>
        )}
      </div>
    </div>
  );
}
