import { useEffect, useState, useRef } from "react";

interface RecoveryTimerProps {
  disabledUntil: string;
  /** If true, shows "Expired" when time runs out. If false, shows empty. Default: false */
  showExpired?: boolean;
  /** Additional CSS classes */
  className?: string;
}

/**
 * Displays a countdown timer for provider recovery.
 * Shows remaining time in "Xm Ys" format.
 */
export function RecoveryTimer({
  disabledUntil,
  showExpired = false,
  className = "",
}: RecoveryTimerProps) {
  const [timeLeft, setTimeLeft] = useState<string>("");
  const timerRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    const calculateTimeLeft = () => {
      const now = new Date().getTime();
      const until = new Date(disabledUntil).getTime();
      const diff = until - now;

      if (diff <= 0) {
        setTimeLeft(showExpired ? "Expired" : "");
        if (timerRef.current) {
          clearInterval(timerRef.current);
          timerRef.current = null;
        }
        return;
      }

      const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60));
      const seconds = Math.floor((diff % (1000 * 60)) / 1000);
      setTimeLeft(`${minutes}m ${seconds}s`);
    };

    calculateTimeLeft();
    timerRef.current = setInterval(calculateTimeLeft, 1000);

    return () => {
      if (timerRef.current) {
        clearInterval(timerRef.current);
        timerRef.current = null;
      }
    };
  }, [disabledUntil, showExpired]);

  if (!timeLeft) return <span className="text-sm text-text-muted">—</span>;

  return (
    <span
      className={`font-mono bg-warning-light text-warning-dark px-2 py-0.5 rounded inline-flex items-center gap-1 ${className}`}
    >
      ⏱️ {timeLeft}
    </span>
  );
}
