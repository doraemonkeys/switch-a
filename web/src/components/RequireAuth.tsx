import type { ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useApi } from "@/api/useApi";

export function RequireAuth({ children }: { children: ReactNode }) {
  const { getToken } = useApi();
  const location = useLocation();

  if (!getToken()) {
    return <Navigate to="/login" state={{ from: location }} replace />;
  }

  return <>{children}</>;
}
