import { useSyncExternalStore, useCallback, useRef } from "react";

/**
 * Uses useSyncExternalStore for React 19 concurrent mode compatibility
 * and automatic cross-tab synchronization via storage events.
 */
export function useLocalStorage<T>(
  key: string,
  initialValue: T,
): [T, (value: T | ((prev: T) => T)) => void] {
  // Use ref to stabilize initialValue reference (avoids re-renders when inline objects/arrays are passed)
  const initialValueRef = useRef(initialValue);
  const snapshotRef = useRef<{
    key: string;
    serializedValue: string | null;
    value: T;
  } | null>(null);

  const subscribe = useCallback(
    (onStoreChange: () => void) => {
      const handleStorageChange = (e: StorageEvent) => {
        if (e.key === key) {
          onStoreChange();
        }
      };

      // Cross-tab sync via native storage event
      window.addEventListener("storage", handleStorageChange);
      // Same-tab sync via custom event
      window.addEventListener(`localStorage:${key}`, onStoreChange);

      return () => {
        window.removeEventListener("storage", handleStorageChange);
        window.removeEventListener(`localStorage:${key}`, onStoreChange);
      };
    },
    [key],
  );

  const getSnapshot = useCallback((): T => {
    let item: string | null = null;
    try {
      item = localStorage.getItem(key);
      const cachedSnapshot = snapshotRef.current;
      if (
        cachedSnapshot?.key === key &&
        cachedSnapshot.serializedValue === item
      ) {
        return cachedSnapshot.value;
      }

      const value = item !== null ? JSON.parse(item) : initialValueRef.current;
      snapshotRef.current = { key, serializedValue: item, value };
      return value;
    } catch (error) {
      console.error(`Error reading localStorage key "${key}":`, error);
      const value = initialValueRef.current;
      snapshotRef.current = { key, serializedValue: item, value };
      return value;
    }
  }, [key]);

  // SSR fallback
  const getServerSnapshot = useCallback((): T => {
    return initialValueRef.current;
  }, []);

  const value = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);

  const setValue = useCallback(
    (newValue: T | ((prev: T) => T)) => {
      try {
        const valueToStore =
          newValue instanceof Function ? newValue(getSnapshot()) : newValue;

        localStorage.setItem(key, JSON.stringify(valueToStore));

        // Trigger same-tab update
        window.dispatchEvent(new Event(`localStorage:${key}`));
      } catch (error) {
        console.error(`Error setting localStorage key "${key}":`, error);
      }
    },
    [key, getSnapshot],
  );

  return [value, setValue];
}
