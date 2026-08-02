import { createContext } from "react";
import type { DebugCaptureStatus, StartDebugCaptureRequest } from "@/api";

export type DebugCaptureOperation = "start" | "stop" | null;

export interface DebugCaptureContextValue {
  status: DebugCaptureStatus | null;
  loading: boolean;
  error: Error | null;
  operation: DebugCaptureOperation;
  refreshStatus: () => Promise<void>;
  startCapture: (input: StartDebugCaptureRequest) => Promise<void>;
  stopCapture: (sessionId: string) => Promise<void>;
}

export const DebugCaptureContext =
  createContext<DebugCaptureContextValue | null>(null);
