import { useEffect, useState } from 'react';

// useNow returns the current time in milliseconds, updated every `every` ms while every is
// set (e.g. for a running timer); null keeps it still.
export function useNow(every: number | null): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    setNow(Date.now());
    if (every === null) return;
    const timer = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(timer);
  }, [every]);
  return now;
}
