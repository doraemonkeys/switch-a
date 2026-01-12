import { BrowserRouter, Routes, Route, Navigate } from "react-router-dom";
import { Layout } from "@/components/Layout";
import { Dashboard } from "@/pages/Dashboard";
import { Providers } from "@/pages/providers";
import { Groups } from "@/pages/Groups";
import { Config } from "@/pages/Config";
import { Logs } from "@/pages/Logs";
import { Login } from "@/pages/Login";
import { RequireAuth } from "@/components/RequireAuth";
import { ApiProvider } from "@/api/ApiContext";

function App() {
  return (
    <ApiProvider>
      <BrowserRouter basename="/admin">
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route
            path="/"
            element={
              <RequireAuth>
                <Layout />
              </RequireAuth>
            }
          >
            <Route index element={<Dashboard />} />
            <Route path="providers" element={<Providers />} />
            <Route path="groups" element={<Groups />} />
            <Route path="config" element={<Config />} />
            <Route path="logs" element={<Logs />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </ApiProvider>
  );
}

export default App;
