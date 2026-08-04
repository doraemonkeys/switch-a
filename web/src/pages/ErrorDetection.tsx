import { useSearchParams } from "react-router";
import { ErrorDetectionFeature } from "@/features/error-detection";
import {
  getErrorDetectionPrefillKey,
  readErrorDetectionPrefill,
} from "./error-detection-prefill";

export function ErrorDetection() {
  const [searchParams] = useSearchParams();
  const prefill = readErrorDetectionPrefill(searchParams);

  // A changed deep link represents a new draft intent; remounting prevents an
  // earlier editor session from silently retaining the wrong provider or protocol.
  return (
    <ErrorDetectionFeature
      key={getErrorDetectionPrefillKey(prefill)}
      prefill={prefill}
    />
  );
}
