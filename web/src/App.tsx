import { BrowserRouter, Navigate, Route, Routes } from "react-router";
import { Layout } from "@/components/Layout";
import { RequireAuth } from "@/components/RequireAuth";
import { Config } from "@/pages/Config";
import { Dashboard } from "@/pages/Dashboard";
import { Groups } from "@/pages/Groups";
import { Login } from "@/pages/Login";
import { Logs } from "@/pages/Logs";
import { Monitor } from "@/pages/Monitor";
import { Providers } from "@/pages/providers";
import { RoutingPolicies } from "@/pages/RoutingPolicies";
import {
  DebugCapturePage,
  DebugCaptureProvider,
} from "@/features/debug-capture";

function AuthenticatedLayout() {
  return (
    <RequireAuth>
      <DebugCaptureProvider>
        <Layout />
      </DebugCaptureProvider>
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
        <Route path="groups" element={<Groups />} />
        <Route path="routing" element={<RoutingPolicies />} />
        <Route path="config" element={<Config />} />
        <Route path="logs" element={<Logs />} />
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
