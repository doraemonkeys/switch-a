import { useContext } from "react";
import { DebugCaptureContext } from "./context";

export function useDebugCapture() {
  const value = useContext(DebugCaptureContext);
  if (!value) {
    throw new Error("useDebugCapture must be used within DebugCaptureProvider");
  }
  return value;
}
