import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { Layout } from "@/components/Layout";
import { RequireAuth } from "@/components/RequireAuth";
import { Config } from "@/pages/Config";
import { Dashboard } from "@/pages/Dashboard";
import { ErrorDetection } from "@/pages/ErrorDetection";
import { Groups } from "@/pages/Groups";
import { Login } from "@/pages/Login";
import { Logs } from "@/pages/Logs";
import { Monitor } from "@/pages/Monitor";
import { TokenUsage } from "@/pages/TokenUsage";
import { Providers } from "@/pages/providers";
import { CredentialSessions } from "@/pages/credentials";
import { RoutingPolicies } from "@/pages/RoutingPolicies";
import { APICatalogProvider } from "@/api";
import {
  DebugCapturePage,
  DebugCaptureProvider,
} from "@/features/debug-capture";

function AuthenticatedLayout() {
  return (
    <RequireAuth>
      <APICatalogProvider>
        <DebugCaptureProvider>
          <Layout />
        </DebugCaptureProvider>
      </APICatalogProvider>
    </RequireAuth>
  );
}

export function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/" element={<AuthenticatedLayout />}>
        <Route index element={<Dashboard />} />
        <Route path="monitor" element={<Monitor />} />
        <Route path="providers" element={<Providers />} />
        <Route path="credentials" element={<CredentialSessions />} />
        <Route path="groups" element={<Groups />} />
        <Route path="routing" element={<RoutingPolicies />} />
        <Route path="error-detection" element={<ErrorDetection />} />
        <Route path="config" element={<Config />} />
        <Route path="logs" element={<Logs />} />
        <Route path="token-usage" element={<TokenUsage />} />
        <Route path="debug-capture" element={<DebugCapturePage />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Route>
    </Routes>
  );
}

function App() {
  return (
    <BrowserRouter basename="/admin">
      <AppRoutes />
    </BrowserRouter>
  );
}

export default App;
