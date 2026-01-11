import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App.tsx";
import { ApiProvider } from "./api";
import { ErrorBoundary } from "./components";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ErrorBoundary>
      <ApiProvider>
        <App />
      </ApiProvider>
    </ErrorBoundary>
  </StrictMode>,
);
